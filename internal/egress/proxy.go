package egress

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// dialTimeout bounds reaching an allowed host, so a black-holed address fails
// the turn quickly instead of hanging it.
const dialTimeout = 30 * time.Second

// Proxy is the forward proxy every workstation's outbound traffic goes through
// (ADR-0028): `CONNECT host:port` for TLS, absolute-form requests for plain
// HTTP. A host and port outside the allowlist are refused before anything is
// dialed, so a blocked call never opens a connection at all.
type Proxy struct {
	allow *Allowlist
	log   *slog.Logger
	dial  func(network, addr string) (net.Conn, error) // nil = net.Dial with a timeout
}

// NewProxy returns a proxy enforcing allow. A nil logger discards.
func NewProxy(allow *Allowlist, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Proxy{allow: allow, log: log}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	p.forward(w, r)
}

// connect tunnels TLS: once the client is told the tunnel is open it speaks a
// protocol the proxy neither reads nor understands, which is the whole point —
// nothing here terminates TLS (ADR-0028).
func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Host
	if target == "" {
		target = r.Host
	}
	host, port, err := splitHostPort(target, "443")
	if err != nil || !p.allow.Allows(host, port) {
		p.refuse(w, r, host, port, err)
		return
	}
	upstream, err := p.dialer()("tcp", net.JoinHostPort(host, port))
	if err != nil {
		p.log.Warn("egress dial failed", "host", host, "err", err)
		http.Error(w, "spool-egress: cannot reach "+host, http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	hj, ok := w.(http.Hijacker)
	if !ok {
		p.log.Error("egress hijack unsupported", "host", host)
		http.Error(w, "spool-egress: tunnel unsupported", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hj.Hijack()
	if err != nil {
		p.log.Error("egress hijack failed", "host", host, "err", err)
		http.Error(w, "spool-egress: tunnel unsupported", http.StatusInternalServerError)
		return
	}
	defer func() { _ = client.Close() }()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	p.log.Debug("egress allowed", "host", host, "port", port, "method", r.Method)

	// A client that sent its first TLS bytes straight after the CONNECT line
	// has them sitting in the hijacked reader already; reading the socket
	// alone would lose exactly the ClientHello.
	fromClient := io.Reader(client)
	if buffered != nil && buffered.Reader.Buffered() > 0 {
		fromClient = io.MultiReader(io.LimitReader(buffered.Reader, int64(buffered.Reader.Buffered())), client)
	}
	// Both directions run until one side is done; the handler does not return
	// while the tunnel is live, so the hijacked socket stays open.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(upstream, fromClient)
	}()
	_, _ = io.Copy(client, upstream)
	<-done
}

// forward relays a plain-HTTP request, which a client sends in absolute form
// (`GET http://host/path`) when it is talking to a proxy.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() || r.URL.Host == "" {
		http.Error(w, "spool-egress: this is a proxy; use an absolute URL or CONNECT", http.StatusBadRequest)
		return
	}
	host, port, err := splitHostPort(r.URL.Host, "80")
	if err != nil || !p.allow.Allows(host, port) {
		p.refuse(w, r, host, port, err)
		return
	}

	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	stripHopByHop(outbound.Header)

	resp, err := p.transport().RoundTrip(outbound)
	if err != nil {
		p.log.Warn("egress request failed", "host", host, "err", err)
		http.Error(w, "spool-egress: cannot reach "+host, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	p.log.Debug("egress allowed", "host", host, "port", port, "method", r.Method)

	stripHopByHop(resp.Header)
	for key, values := range resp.Header {
		w.Header()[key] = values
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// refuse is the whole reason this process exists. The body names the host and
// says who refused, so the agent reads a cause rather than a timeout — and the
// log line records the attempt by host only: a URL can carry a credential in
// its query string, and this log is the one place a blocked exfiltration shows
// up (ADR-0028).
func (p *Proxy) refuse(w http.ResponseWriter, r *http.Request, host, port string, parseErr error) {
	if parseErr != nil {
		http.Error(w, "spool-egress: unreadable target", http.StatusBadRequest)
		return
	}
	p.log.Warn("egress refused", "host", host, "port", port, "method", r.Method)
	http.Error(w, fmt.Sprintf(
		"spool-egress: %s:%s is not on this workstation's egress allowlist, so the request was refused "+
			"(ADR-0028). The allowlist names hosts and ports, so an allowed host on another port is refused too. "+
			"Ask the operator to add it if the work needs it.", host, port),
		http.StatusForbidden)
}

func (p *Proxy) dialer() func(network, addr string) (net.Conn, error) {
	if p.dial != nil {
		return p.dial
	}
	d := &net.Dialer{Timeout: dialTimeout}
	return d.Dial
}

// transport carries plain-HTTP forwarding. It dials through the same hook the
// tunnel uses, so a test can point the whole proxy at a stand-in server.
func (p *Proxy) transport() http.RoundTripper {
	return &http.Transport{
		Dial:                  p.dialer(), //nolint:staticcheck // DialContext's ctx is unused here; one hook serves both paths
		ResponseHeaderTimeout: dialTimeout,
	}
}

// hopByHop headers are meaningful between two adjacent HTTP peers only and
// must not be relayed (RFC 9110 §7.6.1).
var hopByHop = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func stripHopByHop(h http.Header) {
	// Connection names further headers that are themselves hop-by-hop.
	for _, name := range h.Values("Connection") {
		for _, part := range strings.Split(name, ",") {
			h.Del(strings.TrimSpace(part))
		}
	}
	for _, name := range hopByHop {
		h.Del(name)
	}
}

func splitHostPort(target, defaultPort string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(target)
	if err != nil {
		var addrErr *net.AddrError
		if errors.As(err, &addrErr) && strings.Contains(addrErr.Err, "missing port") {
			return target, defaultPort, nil
		}
		return "", "", err
	}
	if host == "" {
		return "", "", &url.Error{Op: "proxy", URL: target, Err: errors.New("no host")}
	}
	return host, port, nil
}
