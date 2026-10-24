package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/roomtext"
	"github.com/enes-alatas/spool/internal/store"
)

// Settings → Cloud (ADR-0056) is the hub's setup for cloud loops,
// as steps the hub checks rather than ones the operator judges. These
// routes are its hub half: the cloud listener's switch, the public address
// and the check that a request to it reaches this hub.

// CloudListener is the cloud listener as Settings → Cloud switches it
// (ADR-0056). cmd/spool owns the socket; httpapi may only ask.
type CloudListener interface {
	// Listening is the address the listener serves on, "" while it is
	// off, and whether --cloud-listen opened it: a listener the flag
	// opened closes only with a restart without the flag.
	Listening() (addr string, fromFlag bool)
	// Open binds addr, a port alone meaning one on loopback, and serves
	// the listener there, and says the address it bound. The hub opens it
	// again at its next start. Open on the address it is already open on
	// changes nothing; on another, it is an error.
	Open(addr string) (string, error)
	// Close stops the listener the page opened, and the hub's next start
	// leaves it off.
	Close() error
}

// DefaultCloudListen is where the page opens the cloud listener when the
// operator names no address: a port on loopback, which the tunnel or
// proxy in front of it reaches.
const DefaultCloudListen = "127.0.0.1:8090"

// CloudCheckEvery is how often the hub checks its public address again.
const CloudCheckEvery = 5 * time.Minute

// cloudCheckTimeout bounds one check from the request to its answer.
const cloudCheckTimeout = 10 * time.Second

// What a cloud check found. A failed check names the stage it stopped at,
// since each asks something different of the operator.
const (
	CloudCheckOK     = "ok"
	CloudCheckFailed = "failed"

	// CloudStageRefused: the stored address is one the hub refuses
	// (CloudAddressAllowed), such as one stored before the rule narrowed.
	// No request is made.
	CloudStageRefused = "refused"
	// CloudStageListenerOff: the cloud listener is off, so nothing on this
	// hub could answer. No request is made.
	CloudStageListenerOff = "listener_off"
	// CloudStageDNS: the address's host doesn't resolve.
	CloudStageDNS = "dns"
	// CloudStageTLS: the host answered, but not with a certificate this
	// machine trusts for that name.
	CloudStageTLS = "tls"
	// CloudStageTimeout: nothing answered within the check's time.
	CloudStageTimeout = "timeout"
	// CloudStageUnreachable: the connection was refused or dropped.
	CloudStageUnreachable = "unreachable"
	// CloudStageOtherServer: something answered, but not this hub's
	// listener with the value it issued.
	CloudStageOtherServer = "other_server"
	// CloudStageSession: the hub's own check passed, but a cloud session
	// never reached the hub at the address. It is the session's verdict,
	// so only a check the operator asks for clears it (ADR-0056).
	CloudStageSession = "session"
)

// CloudRoutePrivate is a pass whose request went to a loopback, private,
// CGNAT or link-local address: it reached the listener from this machine,
// which proves nothing of the internet a cloud session comes from.
const CloudRoutePrivate = "private"

// cloudWarningQuickTunnel marks an address on a Cloudflare quick tunnel,
// whose hostname is reissued once the tunnel stops. It is accepted; the
// page says what the risk is (ADR-0056).
const cloudWarningQuickTunnel = "quick_tunnel"

// codeCloudAddressInvalid is the reason an address is refused.
const codeCloudAddressInvalid = "cloud_address_invalid"

// codeCloudListener is the reason a listener switch is refused.
const codeCloudListener = "cloud_listener"

// CloudCheckResult is the last check of the public address.
type CloudCheckResult struct {
	State string `json:"state"`
	// Stage and Detail say where a failed check stopped, and how.
	Stage  string `json:"stage,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Route is CloudRoutePrivate on a pass that went to an address of a
	// private route, "" otherwise.
	Route     string `json:"route,omitempty"`
	CheckedAt int64  `json:"checked_at"`
	// address is the one checked: a result for another is no result.
	address string
}

// cloudCheckState is the hub's last check, kept in memory: the hub
// checks again as it starts, so nothing needs it from a previous run.
type cloudCheckState struct {
	// running serializes checks, so two never race to record.
	running sync.Mutex
	mu      sync.Mutex
	last    *CloudCheckResult
	// session is a session's verdict that it can't reach the hub, which
	// stands as the last check until the operator asks for one.
	session *CloudCheckResult
}

type cloudSettingsView struct {
	Listener cloudListenerView `json:"listener"`
	// Address is the public address, "" while none is set.
	Address string `json:"address"`
	Warning string `json:"warning,omitempty"`
	// Check is the last check of Address, absent until one has run.
	Check       *CloudCheckResult    `json:"check,omitempty"`
	Account     cloudAccountView     `json:"account"`
	Environment cloudEnvironmentView `json:"environment"`
}

type cloudListenerView struct {
	On       bool   `json:"on"`
	Addr     string `json:"addr,omitempty"`
	FromFlag bool   `json:"from_flag"`
}

// cloudAccountView says whether the account is set, never what it is,
// and how its last check went.
type cloudAccountView struct {
	Set bool `json:"set"`
	// CLI is whether the hub finds the claude CLI it signs in and refreshes
	// with. A hub without it says to install it and reload (ADR-0058).
	CLI bool `json:"cli"`
	// Check is the last account check, absent until one has run.
	Check *CloudAccountCheckResult `json:"check,omitempty"`
}

type cloudAddressReq struct {
	Address string `json:"address"`
}

type cloudListenerReq struct {
	On bool `json:"on"`
	// Listen is where to open it, DefaultCloudListen when empty.
	Listen string `json:"listen"`
}

func (server *Server) handleGetCloudSettings(w http.ResponseWriter, r *http.Request) {
	server.writeCloudSettings(w, r)
}

// handleSetCloudAddress stores the public address and checks it at once,
// so the answer carries the check. The address must be HTTPS: the loop's
// token is sent to it.
func (server *Server) handleSetCloudAddress(w http.ResponseWriter, r *http.Request) {
	var req cloudAddressReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	address, err := CloudAddressOrigin(req.Address, server.CloudLoopbackAddress)
	if err != nil {
		server.jsonErrCode(w, 400, codeCloudAddressInvalid, "%v", err)
		return
	}
	server.changeCloudAddress(w, r, address)
}

func (server *Server) handleClearCloudAddress(w http.ResponseWriter, r *http.Request) {
	server.changeCloudAddress(w, r, "")
}

// changeCloudAddress stores address, "" for none, and checks it.
func (server *Server) changeCloudAddress(w http.ResponseWriter, r *http.Request, address string) {
	if err := server.Store.Settings().Set(r.Context(), store.SettingCloudAddress, address); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.askCloudCheck(r.Context())
	server.writeCloudSettings(w, r)
}

func (server *Server) handleCheckCloudAddress(w http.ResponseWriter, r *http.Request) {
	server.askCloudCheck(r.Context())
	server.writeCloudSettings(w, r)
}

// handleSwitchCloudListener opens or closes the cloud listener
// (ADR-0056), and checks the address again, since whether it can answer
// just changed.
func (server *Server) handleSwitchCloudListener(w http.ResponseWriter, r *http.Request) {
	if server.CloudListener == nil {
		server.jsonErrCode(w, 409, codeCloudListener, "this hub has no cloud listener to switch")
		return
	}
	var req cloudListenerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	addr, fromFlag := server.CloudListener.Listening()
	switch {
	case req.On && (addr == "" || req.Listen != ""):
		listen := req.Listen
		if listen == "" {
			listen = DefaultCloudListen
		}
		if _, err := server.CloudListener.Open(listen); err != nil {
			server.jsonErrCode(w, 409, codeCloudListener, "%v", err)
			return
		}
	case !req.On && fromFlag:
		server.jsonErrCode(w, 409, codeCloudListener,
			"the cloud listener was opened with `--cloud-listen`; restart the hub without the flag to close it")
		return
	case !req.On && addr != "":
		if err := server.CloudListener.Close(); err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
	}
	server.askCloudCheck(r.Context())
	server.writeCloudSettings(w, r)
}

func (server *Server) writeCloudSettings(w http.ResponseWriter, r *http.Request) {
	view, err := server.cloudSettings(r.Context(), callerOf(r))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

// cloudSettings is Settings → Cloud as who reads it. A member sees where a
// failed check stopped, not its detail (seesCloudCheckDetail).
func (server *Server) cloudSettings(ctx context.Context, who *caller) (cloudSettingsView, error) {
	var view cloudSettingsView
	if server.CloudListener != nil {
		addr, fromFlag := server.CloudListener.Listening()
		view.Listener = cloudListenerView{On: addr != "", Addr: addr, FromFlag: fromFlag}
	}
	address, err := server.storedCloudAddress(ctx)
	if err != nil {
		return view, err
	}
	view.Address = address
	if address != "" {
		view.Warning = cloudAddressWarning(address)
		view.Check = server.cloudCheck.result(address)
		if view.Check != nil && !seesCloudCheckDetail(who) {
			view.Check.Detail = ""
		}
	}
	set, err := server.cloudAccountSet(ctx)
	if err != nil {
		return view, err
	}
	view.Account.Set = set
	view.Account.CLI = server.CloudSignIn != nil && server.CloudSignIn.Available()
	view.Account.Check = server.cloudAccountCheck.result()
	if view.Account.Check != nil && !seesCloudCheckDetail(who) {
		view.Account.Check.Detail = ""
	}
	if view.Environment, err = server.cloudEnvironment(ctx); err != nil {
		return view, err
	}
	return view, nil
}

// storedCloudAddress is the public address as stored, "" for none.
func (server *Server) storedCloudAddress(ctx context.Context) (string, error) {
	address, err := server.Store.Settings().Get(ctx, store.SettingCloudAddress)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return address, err
}

// CheckCloudAddress checks that a request to the public address reaches
// this hub's cloud listener, and records what it found. While no address
// is set it checks nothing, and cloud loops are held. The hub runs it when
// the address or the listener changes, on the page's ask, every
// CloudCheckEvery, and when CloudGate asks for one before a cloud loop
// wakes. While a session's verdict stands on the address
// (CloudSessionUnreached), it makes no request and the verdict stays the
// last check: only askCloudCheck sets it aside.
func (server *Server) CheckCloudAddress(ctx context.Context) {
	server.cloudCheck.running.Lock()
	defer server.cloudCheck.running.Unlock()
	address, err := server.storedCloudAddress(ctx)
	if err != nil {
		server.Log.Warn("cloud address unread; not checked", "err", err)
		return
	}
	if address == "" {
		server.cloudCheck.record(nil)
		if server.CloudGate != nil {
			server.CloudGate.Clear()
		}
		return
	}
	if verdict := server.cloudCheck.sessionVerdict(address); verdict != nil {
		if server.CloudGate != nil {
			server.CloudGate.Record(address, false, verdict.CheckedAt)
		}
		return
	}
	result := server.checkCloudAddress(ctx, address)
	if result.State != CloudCheckOK {
		server.Log.Warn("cloud address check failed", "address", address, "stage", result.Stage, "detail", result.Detail)
	}
	server.cloudCheck.record(&result)
	if server.CloudGate != nil {
		server.CloudGate.Record(address, result.State == CloudCheckOK, result.CheckedAt)
	}
}

// askCloudCheck is a check the operator asked for, from the page or by
// changing the address or the listener: it sets a session's verdict aside
// and checks again.
func (server *Server) askCloudCheck(ctx context.Context) {
	server.cloudCheck.dropSession()
	server.CheckCloudAddress(ctx)
}

// CloudSessionUnreached takes a cloud session's word that it can't reach
// the hub: its start posted nothing within within (cloud.Runtime's
// Unreached). The hub's own check can pass where the session's request
// can't, over a private route such as a tailnet's, so the session's word
// wins: the step turns red and every cloud loop is held, until the operator
// asks for a check (ADR-0056).
func (server *Server) CloudSessionUnreached(loopID string, within time.Duration) {
	server.cloudCheck.running.Lock()
	defer server.cloudCheck.running.Unlock()
	address, err := server.storedCloudAddress(context.Background())
	if err != nil || address == "" {
		return
	}
	result := CloudCheckResult{
		State: CloudCheckFailed, Stage: CloudStageSession,
		Detail: fmt.Sprintf("a cloud session was created and posted nothing to the hub within %s, "+
			"so it can't reach the hub at this address, though this machine can", within),
		CheckedAt: time.Now().UnixMilli(),
		address:   address,
	}
	server.Log.Warn("cloud session never reached the hub", "loop", loopID, "address", address)
	server.cloudCheck.holdSession(&result)
	if server.CloudGate != nil {
		server.CloudGate.Record(address, false, result.CheckedAt)
	}
}

// RunCloudChecks checks the public address now and every interval after,
// and whenever CloudGate asks before a cloud loop wakes, until ctx ends.
func (server *Server) RunCloudChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var rechecks <-chan struct{}
	if server.CloudGate != nil {
		rechecks = server.CloudGate.Rechecks()
	}
	for {
		server.CheckCloudAddress(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-rechecks:
		}
	}
}

// checkCloudAddress issues a value and requests it through address. Only
// this hub's cloud listener answers 204 to it, and only once.
func (server *Server) checkCloudAddress(ctx context.Context, address string) CloudCheckResult {
	result := CloudCheckResult{State: CloudCheckFailed, CheckedAt: time.Now().UnixMilli(), address: address}
	if err := CloudAddressAllowed(address, server.CloudLoopbackAddress); err != nil {
		result.Stage, result.Detail = CloudStageRefused, err.Error()
		return result
	}
	if server.CloudListener == nil {
		result.Stage, result.Detail = CloudStageListenerOff, "this hub has no cloud listener"
		return result
	}
	if addr, _ := server.CloudListener.Listening(); addr == "" {
		result.Stage, result.Detail = CloudStageListenerOff, "the cloud listener is off, so nothing on this hub can answer"
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, cloudCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+cloudCheckPrefix+server.IssueCloudCheck(), nil)
	if err != nil {
		result.Stage, result.Detail = CloudStageUnreachable, err.Error()
		return result
	}
	var reached net.Addr
	req = req.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reached = info.Conn.RemoteAddr() },
	}))
	resp, err := cloudCheckClient.Do(req)
	if err != nil {
		stage, detail := cloudCheckFailure(err)
		// a TLS or DNS error quotes what the far end answered (#959)
		result.Stage, result.Detail = stage, roomtext.Plain(detail)
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusNoContent {
		result.Stage = CloudStageOtherServer
		result.Detail = fmt.Sprintf("the address answered %d, not this hub's cloud listener", resp.StatusCode)
		return result
	}
	result.State = CloudCheckOK
	if privateRoute(reached) {
		result.Route = CloudRoutePrivate
	}
	return result
}

// cgnat is the shared address space of RFC 6598, which tailnets number
// their machines from.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// privateRoute reports whether addr is one a request from this machine can
// reach without crossing the internet: loopback, private (fc00::/7 holds
// Tailscale's), CGNAT, link-local or unspecified.
// Behind HTTPS_PROXY the address is the proxy's, so a proxy on a private
// address makes every pass read private: a label only, which wakes loops
// all the same.
func privateRoute(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	ip, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

// cloudCheckClient follows no redirect: an address that sends the check
// elsewhere would send a loop's token there too.
var cloudCheckClient = &http.Client{
	Timeout:       cloudCheckTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// cloudCheckFailure names the stage a failed request stopped at, and how.
// The URL is left out of the detail: it carries the check's value.
func cloudCheckFailure(err error) (stage, detail string) {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var (
		dnsErr      *net.DNSError
		verifyErr   *tls.CertificateVerificationError
		unknownCA   x509.UnknownAuthorityError
		wrongHost   x509.HostnameError
		invalidCert x509.CertificateInvalidError
		recordErr   tls.RecordHeaderError
		alertErr    tls.AlertError
		netErr      net.Error
	)
	switch {
	case errors.As(err, &dnsErr):
		return CloudStageDNS, err.Error()
	case errors.As(err, &verifyErr), errors.As(err, &unknownCA), errors.As(err, &wrongHost),
		errors.As(err, &invalidCert), errors.As(err, &recordErr), errors.As(err, &alertErr):
		return CloudStageTLS, err.Error()
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return CloudStageTimeout, err.Error()
	}
	return CloudStageUnreachable, err.Error()
}

func (state *cloudCheckState) record(result *CloudCheckResult) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.last = result
}

// holdSession records a session's verdict as the last check, standing
// until dropSession.
func (state *cloudCheckState) holdSession(result *CloudCheckResult) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.last, state.session = result, result
}

// sessionVerdict is the session's verdict standing on address, nil when
// there is none. One on another address no longer stands.
func (state *cloudCheckState) sessionVerdict(address string) *CloudCheckResult {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.session == nil || state.session.address != address {
		state.session = nil
		return nil
	}
	verdict := *state.session
	return &verdict
}

func (state *cloudCheckState) dropSession() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.session = nil
}

// result is the last check of address, nil when none has run on it.
func (state *cloudCheckState) result(address string) *CloudCheckResult {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.last == nil || state.last.address != address {
		return nil
	}
	result := *state.last
	return &result
}

// CloudAddressOrigin reads the public address an operator pasted into the
// origin the hub stores: scheme and host, no path, and allowed
// (CloudAddressAllowed).
func CloudAddressOrigin(raw string, loopback bool) (string, error) {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "/")
	if err := CloudAddressAllowed(raw, loopback); err != nil {
		return "", err
	}
	parsed, _ := url.Parse(raw)
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%q: the address alone, with no path, query or user", raw)
	}
	return parsed.Scheme + "://" + strings.ToLower(parsed.Host), nil
}

// CloudAddressAllowed refuses a public address no cloud session could
// reach the hub at, or one a loop's token would cross in the clear: it
// must be HTTPS. Plain HTTP on loopback is allowed only when loopback is
// set, which only tests do: the hub would check it green from itself,
// though nothing outside this machine reaches it.
func CloudAddressAllowed(raw string, loopback bool) error {
	if loopback {
		return HTTPSOrLoopback(raw)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%q is not a URL", raw)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("%q must be https: a cloud session reaches the hub over the internet, and the loop's token is sent there", raw)
	}
	return nil
}

// HTTPSOrLoopback refuses a URL a credential would cross in the clear: one
// that is neither HTTPS nor plain HTTP to this machine's loopback.
func HTTPSOrLoopback(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%q is not a URL", raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(parsed.Hostname()); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("%q must be https, or http on a loopback address", raw)
}

// cloudAddressWarning names a risk in an accepted address, "" for none.
func cloudAddressWarning(address string) string {
	parsed, err := url.Parse(address)
	if err == nil && strings.HasSuffix(parsed.Hostname(), ".trycloudflare.com") {
		return cloudWarningQuickTunnel
	}
	return ""
}
