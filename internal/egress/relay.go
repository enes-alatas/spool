package egress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// The relay's bounds (ADR-0064). Claude Code batches its spans every 5 s
// and a turn is about 40 KB, so neither the body nor the rate bound is
// near a working loop; they are there so a loop can't make the proxy hold
// what it shouldn't.
const (
	// RelayPort is where the relay listens on every network the proxy is
	// on: OTLP/HTTP's own port.
	RelayPort = "4318"

	// RelayPath is the one path the relay takes.
	RelayPath = "/v1/traces"

	// LangfuseTracesPath is Langfuse's OTLP traces path under its address:
	// the relay's upstream, or the read address a bare loop exports to.
	LangfuseTracesPath = "/api/public/otel/v1/traces"

	maxRelayBody        = 4 << 20
	relayPerMinute      = 60
	relayInFlight       = 4
	relayUpstreamWait   = 30 * time.Second
	relayHeaderTimeout  = 10 * time.Second
	relayReadTimeout    = 30 * time.Second
	relayUpstreamDrain  = 64 << 10
	relayIdentityPrefix = "spool.loop."
)

// Relay is the egress proxy's trace relay (ADR-0064): a loop's Claude Code
// exports its traces to it, and it forwards them to the operator's
// Langfuse with the ingest key the loop never holds, stamped with the loop
// they came from. It takes POST /v1/traces in OTLP/JSON and nothing else,
// and answers with the upstream's status alone, so a loop can write spans
// through it and read nothing back.
type Relay struct {
	config *RelayConfig
	log    *slog.Logger
	client *http.Client

	// subnet finds the network an address of the proxy's own is on; nil
	// reads the proxy's interfaces.
	subnet func(local netip.Addr) (netip.Prefix, bool)
	now    func() time.Time

	mu      sync.Mutex
	budgets map[string]*relayBudget // by loop ID
}

// relayBudget is one loop's share of the relay: requests in the current
// minute, and requests in flight.
type relayBudget struct {
	minute   time.Time
	count    int
	inFlight int
}

// NewRelay returns a relay reading its configuration from config. A nil
// logger discards.
func NewRelay(config *RelayConfig, log *slog.Logger) *Relay {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Relay{
		config: config,
		log:    log,
		client: &http.Client{
			Timeout: relayUpstreamWait,
			// the one upstream the operator set, and nowhere a redirect
			// would send the ingest key
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				Proxy:                 nil,
				ResponseHeaderTimeout: relayUpstreamWait,
				MaxIdleConnsPerHost:   relayInFlight,
			},
		},
		now:     time.Now,
		budgets: map[string]*relayBudget{},
	}
}

// Server is the relay's listener, with read and header timeouts, so a
// slow loop can't hold the proxy's connections.
func (relay *Relay) Server(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           relay,
		ReadHeaderTimeout: relayHeaderTimeout,
		ReadTimeout:       relayReadTimeout,
		WriteTimeout:      relayUpstreamWait + relayReadTimeout,
		MaxHeaderBytes:    64 << 10,
	}
}

func (relay *Relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != RelayPath {
		relay.refuse(w, http.StatusNotFound, "path")
		return
	}
	if r.Method != http.MethodPost {
		relay.refuse(w, http.StatusMethodNotAllowed, "method")
		return
	}
	if encoding := r.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		relay.refuse(w, http.StatusUnsupportedMediaType, "content encoding")
		return
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		relay.refuse(w, http.StatusUnsupportedMediaType, "content type")
		return
	}
	config := relay.config.Current()
	if config.Upstream == "" {
		relay.refuse(w, http.StatusServiceUnavailable, "no upstream")
		return
	}
	loop, ok := relay.identify(r, config.Loops)
	if !ok {
		relay.refuse(w, http.StatusForbidden, "not a loop network")
		return
	}
	release, ok := relay.admit(loop.ID)
	if !ok {
		relay.refuse(w, http.StatusTooManyRequests, "over budget", "loop", loop.ID)
		return
	}
	defer release()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRelayBody))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		relay.refuse(w, http.StatusRequestEntityTooLarge, "body too large", "loop", loop.ID)
		return
	case err != nil:
		relay.refuse(w, http.StatusBadRequest, "body unreadable", "loop", loop.ID)
		return
	}
	stamped, err := StampTraces(body, loop)
	if err != nil {
		relay.refuse(w, http.StatusBadRequest, "not OTLP/JSON traces", "loop", loop.ID)
		return
	}
	w.WriteHeader(relay.forward(r.Context(), config, stamped, loop))
}

// forward sends stamped traces upstream and returns the status to answer
// the loop with: the upstream's, or 502 when it can't be reached.
func (relay *Relay) forward(ctx context.Context, config RelayFile, body []byte, loop RelayLoop) int {
	upstream, err := relayUpstream(config.Upstream)
	if err != nil {
		return http.StatusBadGateway
	}
	upstream.Path = strings.TrimRight(upstream.Path, "/") + LangfuseTracesPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.String(), bytes.NewReader(body))
	if err != nil {
		return http.StatusBadGateway
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", config.Authorization)
	response, err := relay.client.Do(request)
	if err != nil {
		relay.log.Warn("relay upstream unreachable", "loop", loop.ID, "err", err)
		return http.StatusBadGateway
	}
	// Nothing of the response goes back: a loop reads no body and no
	// header of Langfuse's, even in an error.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, relayUpstreamDrain))
	_ = response.Body.Close()
	relay.log.Debug("relay forwarded", "loop", loop.ID, "status", response.StatusCode)
	return response.StatusCode
}

// identify finds the loop a request came from by the network it came in
// on: the proxy's own address it reached, which only that loop's
// workstation shares a network with (ADR-0028). The sender must also be on
// that address's subnet. A workstation that addressed the proxy's address
// on another loop's network would arrive on its own, so it is refused
// rather than taken for the other loop.
func (relay *Relay) identify(r *http.Request, loops []RelayLoop) (RelayLoop, bool) {
	localAddr, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if localAddr == nil {
		return RelayLoop{}, false
	}
	local, err := netip.ParseAddrPort(localAddr.String())
	if err != nil {
		return RelayLoop{}, false
	}
	remote, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return RelayLoop{}, false
	}
	at := local.Addr().Unmap()
	for _, loop := range loops {
		address, err := netip.ParseAddr(loop.Address)
		if err != nil || address.Unmap() != at {
			continue
		}
		subnet, ok := relay.subnetOf(at)
		if !ok || !subnet.Contains(remote.Addr().Unmap()) {
			return RelayLoop{}, false
		}
		return loop, true
	}
	return RelayLoop{}, false
}

func (relay *Relay) subnetOf(local netip.Addr) (netip.Prefix, bool) {
	if relay.subnet != nil {
		return relay.subnet(local)
	}
	return interfaceSubnet(local)
}

// interfaceSubnet is the subnet of the interface that holds local.
func interfaceSubnet(local netip.Addr) (netip.Prefix, bool) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return netip.Prefix{}, false
	}
	for _, addr := range addrs {
		network, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(network.IP)
		if !ok || ip.Unmap() != local {
			continue
		}
		ones, _ := network.Mask.Size()
		return netip.PrefixFrom(local, ones).Masked(), true
	}
	return netip.Prefix{}, false
}

// admit takes a request out of the loop's budget, or reports it over: 60 a
// minute and a few in flight. release gives the in-flight slot back.
func (relay *Relay) admit(loopID string) (release func(), ok bool) {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	minute := relay.now().Truncate(time.Minute)
	budget := relay.budgets[loopID]
	if budget == nil {
		budget = &relayBudget{}
		relay.budgets[loopID] = budget
	}
	if !budget.minute.Equal(minute) {
		budget.minute, budget.count = minute, 0
	}
	if budget.count >= relayPerMinute || budget.inFlight >= relayInFlight {
		return nil, false
	}
	budget.count++
	budget.inFlight++
	return func() {
		relay.mu.Lock()
		defer relay.mu.Unlock()
		budget.inFlight--
	}, true
}

// refuse answers with a status and no body of substance. The log line
// names why, never the request's content.
func (relay *Relay) refuse(w http.ResponseWriter, status int, why string, attrs ...any) {
	relay.log.Warn("relay refused", append([]any{"why", why, "status", status}, attrs...)...)
	w.WriteHeader(status)
}
