package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/enes-alatas/spool/internal/githubapp"
	"github.com/enes-alatas/spool/internal/store"
)

// A connection check (#1323) tells an admin, right after a save, whether
// the connection works, rather than leaving it to a loop's first session.
// An http mcp-server is sent an MCP initialize and a github-app mints a
// token. The hub makes the request itself, which for an http server is
// the path a loop's tool calls take too: the hub brokers every one it
// checks (ADR-0043). Enes chose the shape on 2026-11-05, on #1323.
//
// The dial is guarded, since it is a request the hub makes to an address
// an admin typed: https, or http to this machine; never a link-local or
// listed cloud metadata address, judged on the address the name resolves to as
// it is dialled; no redirect followed; and an answer read only as far as
// telling an MCP server from anything else. What comes back is a coarse
// reason, never the server's body or headers.

const (
	codeConnectionCheckThrottled = "connection_check_throttled"

	// checkTimeout bounds a whole check, the dial and the answer.
	checkTimeout = 10 * time.Second
	// checkAnswerLimit bounds what is read of a server's answer.
	checkAnswerLimit = 64 << 10
	// checksPerMinute is how many checks one caller may start in a minute.
	checksPerMinute = 10
)

// A check's status: ok, failed, unverified (the server answered but the
// secret wasn't sent, so it is untested), or skipped (nothing the hub can
// check).
const (
	checkOK         = "ok"
	checkFailed     = "failed"
	checkUnverified = "unverified"
	checkSkipped    = "skipped"
)

// A check's reason, one an admin can act on.
const (
	reasonOK                = "ok"
	reasonUnreachable       = "unreachable"
	reasonTimedOut          = "timed_out"
	reasonTLSFailed         = "tls_failed"
	reasonRedirected        = "redirected"
	reasonSecretRefused     = "secret_refused"
	reasonSecretUntested    = "secret_untested"
	reasonCredentialMissing = "credential_missing"
	reasonNotMCP            = "not_mcp"
	reasonHTTPError         = "http_error"
	reasonAddressRefused    = "address_refused"
	reasonCleartext         = "cleartext"
	reasonWorkstationLocal  = "workstation_local"
	reasonNotCheckable      = "not_checkable"
	reasonGitHubRefused     = "github_refused"
)

// connectionCheckView is POST /api/connections/{name}/check.
type connectionCheckView struct {
	Connection string `json:"connection"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	// SecretSent says whether the check carried the connection's secret:
	// only when a loop holding it would have the hub send it there.
	SecretSent bool  `json:"secret_sent"`
	CheckedAt  int64 `json:"checked_at"`
}

// handleCheckConnection checks a connection and answers with what it
// found. A failed check is a 200 with status failed: the check worked,
// the connection didn't.
func (server *Server) handleCheckConnection(w http.ResponseWriter, r *http.Request) {
	connection, err := server.Store.Connections().Get(r.Context(), r.PathValue("name"))
	if err == nil && connection.RevokedAt != 0 {
		err = store.ErrConnectionRevoked
	}
	if err != nil {
		server.connectionErr(w, r, err)
		return
	}
	if !server.connectionChecks.allow(checkerOf(r), time.Now()) {
		server.jsonErrCode(w, http.StatusTooManyRequests, codeConnectionCheckThrottled,
			"at most %d connection checks a minute; try again shortly", checksPerMinute)
		return
	}
	view := connectionCheckView{Connection: connection.Name}
	switch {
	case connection.Kind == store.ConnectionMCPServer && connection.Config.Transport == store.MCPTransportHTTP:
		sendSecret, checkable, err := server.checkSendsSecret(r.Context(), connection)
		if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		if checkable {
			view = checkMCPServer(r.Context(), connection, sendSecret, nil)
		} else {
			view = connectionCheckView{Connection: connection.Name, Status: checkSkipped, Reason: reasonWorkstationLocal,
				Message: "a loopback server is checked only when a bare loop holds it: for a contained loop it runs inside the workstation, which the hub can't reach"}
		}
	case connection.Kind == store.ConnectionGitHubApp && server.GitHubApps != nil:
		view = checkGitHubApp(r.Context(), server.GitHubApps, connection)
	case connection.Kind == store.ConnectionMCPServer:
		view = connectionCheckView{Connection: connection.Name, Status: checkSkipped, Reason: reasonNotCheckable,
			Message: "a stdio server runs inside a loop's workstation; it shows on the loop's page from its next session"}
	default:
		view = connectionCheckView{Connection: connection.Name, Status: checkSkipped, Reason: reasonNotCheckable,
			Message: "an env-var is a value the hub can't check"}
	}
	view.CheckedAt = time.Now().UnixMilli()
	// the reason alone: never the URL, which may carry a query, nor the secret
	server.Log.Info("connection check", "connection", connection.Name, "status", view.Status, "reason", view.Reason, "secret_sent", view.SecretSent)
	writeJSON(w, 200, view)
}

// checkSendsSecret reports whether a check of an http mcp-server carries
// its secret, and whether the hub can check it at all (checkReach).
func (server *Server) checkSendsSecret(ctx context.Context, connection *store.Connection) (sendSecret, checkable bool, err error) {
	var loops []*store.Loop
	if len(connection.LoopIDs) > 0 {
		if loops, err = server.Store.Loops().List(ctx); err != nil {
			return false, false, err
		}
	}
	sendSecret, checkable = checkReach(connection, loops)
	return sendSecret, checkable, nil
}

// checkReach is checkSendsSecret over the hub's loops. The secret goes
// only where a loop's own use would have the hub send it: some live loop
// holding the connection, neither archived nor cloud, has the hub broker
// it (store.Connection.Brokered). A loopback server the hub brokers for
// no such loop names a place inside a workstation, and the hub's own
// loopback is another machine's answer, so it isn't checked.
func checkReach(connection *store.Connection, loops []*store.Loop) (sendSecret, checkable bool) {
	for _, loopRecord := range loops {
		if loopRecord.Runtime == store.RuntimeCloud || loopRecord.Status == store.StatusArchived ||
			!slices.Contains(connection.LoopIDs, loopRecord.ID) {
			continue
		}
		if connection.Brokered(loopRecord.Runtime) {
			return connection.Secret != "", true
		}
	}
	return false, !connection.Config.Loopback()
}

// checkMCPServer sends an http mcp-server an MCP initialize, with its
// secret when sendSecret, and reads whether an MCP server answered.
// transport, when not nil, replaces the guarded one: tests only.
func checkMCPServer(ctx context.Context, connection *store.Connection, sendSecret bool, transport http.RoundTripper) connectionCheckView {
	view := connectionCheckView{Connection: connection.Name}
	result := func(status, reason, message string) connectionCheckView {
		view.Status, view.Reason, view.Message = status, reason, message
		return view
	}
	target, err := url.Parse(connection.Config.URL)
	if err != nil {
		return result(checkFailed, reasonUnreachable, "the url doesn't parse")
	}
	loopback := connection.Config.Loopback()
	if target.Scheme != "https" && !loopback {
		return result(checkSkipped, reasonCleartext, "plain http off this machine isn't checked: use an https url")
	}
	if transport == nil {
		transport = guardedTransport(loopback)
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       checkTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(initializeRequest()))
	if err != nil {
		return result(checkFailed, reasonUnreachable, "the url doesn't make a request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "spool-connection-check")
	// a server stored before plain http off loopback was refused is never
	// sent its secret in the clear, as the broker never sends it
	if sendSecret && connection.Secret != "" && !connection.Config.Cleartext() {
		req.Header.Set("Authorization", "Bearer "+connection.Secret)
		view.SecretSent = true
	}
	resp, err := client.Do(req)
	if err != nil {
		reason, message := dialFailure(err)
		return result(checkFailed, reason, message)
	}
	defer resp.Body.Close()
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		defer endMCPSession(client, req, session)
	}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return result(checkFailed, reasonRedirected, "the server redirected the check, and the hub follows no redirect: use the url it redirects to")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		switch {
		case view.SecretSent:
			return result(checkFailed, reasonSecretRefused, fmt.Sprintf("the server refused the secret (HTTP %d)", resp.StatusCode))
		case connection.Secret == "":
			return result(checkFailed, reasonCredentialMissing, fmt.Sprintf("the server asks for a credential (HTTP %d), and the connection has none", resp.StatusCode))
		default:
			return result(checkUnverified, reasonSecretUntested, "reached the server, which asks for a credential; the secret is untested until a loop holds the connection")
		}
	case resp.StatusCode != http.StatusOK:
		return result(checkFailed, reasonHTTPError, fmt.Sprintf("the server answered HTTP %d", resp.StatusCode))
	}
	if !initializeAnswered(resp) {
		return result(checkFailed, reasonNotMCP, "the server answered, but not as an MCP server")
	}
	if connection.Secret != "" && !view.SecretSent {
		return result(checkUnverified, reasonSecretUntested, "reached an MCP server without the secret; the secret is untested until a loop holds the connection")
	}
	return result(checkOK, reasonOK, "an MCP server answered")
}

// initializeRequest is the MCP initialize a check sends.
func initializeRequest() []byte {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "spool-connection-check", "version": "1"},
		},
	})
	return body
}

// initializeAnswered reports whether resp is an MCP server's answer to an
// initialize: a JSON-RPC result naming a protocol version, as JSON or as
// the first event of a stream.
func initializeAnswered(resp *http.Response) bool {
	body := io.LimitReader(resp.Body, checkAnswerLimit)
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var raw []byte
	switch mediaType {
	case "application/json":
		raw, _ = io.ReadAll(body)
	case "text/event-stream":
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 4096), checkAnswerLimit)
		var data []string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" && len(data) > 0 {
				break
			}
			if rest, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(rest, " "))
			}
		}
		raw = []byte(strings.Join(data, "\n"))
	default:
		return false
	}
	var answer struct {
		JSONRPC string `json:"jsonrpc"`
		Result  struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	return json.Unmarshal(raw, &answer) == nil && answer.JSONRPC == "2.0" && answer.Result.ProtocolVersion != ""
}

// endMCPSession ends the session a server opened for the check, best
// effort, with the credential the check was sent with.
func endMCPSession(client *http.Client, sent *http.Request, session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, sent.URL.String(), nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", session)
	if auth := sent.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// errAddressRefused is the guard refusing the address a name resolved to.
var errAddressRefused = errors.New("address refused")

// guardedTransport dials only an address a check may reach: for a
// loopback url, loopback alone; for any other, no loopback, unspecified,
// link-local, multicast or cloud metadata address. It is judged on the
// address the dial connects to, after resolution, so a name that resolves
// somewhere else at dial time than when it was saved gains nothing. It
// ignores HTTPS_PROXY: through a proxy the guard would judge the proxy.
func guardedTransport(loopback bool) http.RoundTripper {
	dialer := &net.Dialer{
		Timeout: checkTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			addrPort, err := netip.ParseAddrPort(address)
			if err != nil || !checkMayDial(addrPort.Addr(), loopback) {
				return errAddressRefused
			}
			return nil
		},
	}
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		TLSHandshakeTimeout:    checkTimeout,
		ResponseHeaderTimeout:  checkTimeout,
		MaxResponseHeaderBytes: checkAnswerLimit,
		DisableKeepAlives:      true,
	}
}

// metadataAddrs are the cloud instance metadata addresses outside
// link-local, which the guard refuses by range: AWS's on IPv6, a
// unique-local address, and Alibaba Cloud's, in the shared address space.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("fd00:ec2::254"),
	netip.MustParseAddr("100.100.100.200"),
}

// checkMayDial reports whether a check may connect to addr. For a
// loopback url that is this machine, an unspecified address included, as
// ConnectionConfig.Loopback counts it.
func checkMayDial(addr netip.Addr, loopback bool) bool {
	addr = addr.Unmap()
	if loopback {
		return addr.IsLoopback() || addr.IsUnspecified()
	}
	return addr.IsValid() && !addr.IsLoopback() && !addr.IsUnspecified() &&
		!addr.IsLinkLocalUnicast() && !addr.IsLinkLocalMulticast() && !addr.IsMulticast() &&
		!slices.Contains(metadataAddrs, addr)
}

// dialFailure is a failed request as a reason and its sentence: how it
// failed, never where it got to.
func dialFailure(err error) (reason, message string) {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	var recordErr tls.RecordHeaderError
	var alertErr tls.AlertError
	switch {
	case errors.Is(err, errAddressRefused):
		return reasonAddressRefused, "the url resolves to an address the hub doesn't check: link-local, cloud metadata, or this machine for a url that doesn't name it"
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return reasonTimedOut, fmt.Sprintf("no answer within %s", checkTimeout)
	case errors.As(err, &dnsErr):
		return reasonUnreachable, "the host name doesn't resolve"
	case errors.As(err, &certErr) || errors.As(err, &unknownAuthority) || errors.As(err, &hostnameErr) ||
		errors.As(err, &invalidCert) || errors.As(err, &recordErr) || errors.As(err, &alertErr):
		return reasonTLSFailed, "the TLS handshake failed: the server's certificate isn't one the hub trusts for that name, or it doesn't speak https"
	case errors.Is(err, syscall.ECONNREFUSED):
		return reasonUnreachable, "the connection was refused: nothing listens there"
	default:
		return reasonUnreachable, "the server couldn't be reached"
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// checkGitHubApp mints the connection's installation token, or answers
// from the cache when one was minted with this key and installation. The
// token stays cached for the loops that hold the app; the check hands it
// to nobody.
func checkGitHubApp(ctx context.Context, minter *githubapp.Minter, connection *store.Connection) connectionCheckView {
	view := connectionCheckView{Connection: connection.Name}
	_, err := minter.Token(ctx, connection)
	var refused *githubapp.RefusedError
	switch {
	case err == nil:
		view.Status, view.Reason, view.Message = checkOK, reasonOK, "GitHub minted an installation token"
	case errors.As(err, &refused):
		view.Status, view.Reason = checkFailed, reasonGitHubRefused
		view.Message = fmt.Sprintf("GitHub refused the mint (HTTP %d): %s%s", refused.Status, refused.Message, githubHint(refused.Status))
	default:
		view.Status, view.Reason, view.Message = checkFailed, reasonUnreachable, "GitHub couldn't be reached to mint a token"
	}
	return view
}

// githubHint says what a mint refusal usually means.
func githubHint(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return " (the app id or the private key is wrong, or the key was deleted)"
	case http.StatusNotFound:
		return " (no such installation of this app: check the installation id)"
	case http.StatusForbidden:
		return " (the installation is suspended, or the app lacks a permission)"
	}
	return ""
}

// checkLimiter caps how many checks each caller starts a minute.
type checkLimiter struct {
	mu     sync.Mutex
	starts map[string][]time.Time
}

// allow records a check by who at now and reports whether it is within
// the cap.
func (limiter *checkLimiter) allow(who string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.starts == nil {
		limiter.starts = map[string][]time.Time{}
	}
	recent := limiter.starts[who][:0]
	for _, at := range limiter.starts[who] {
		if now.Sub(at) < time.Minute {
			recent = append(recent, at)
		}
	}
	if len(recent) >= checksPerMinute {
		limiter.starts[who] = recent
		return false
	}
	limiter.starts[who] = append(recent, now)
	return true
}

// checkerOf is who a check is counted against: the signed-in user, or the
// operator token.
func checkerOf(r *http.Request) string {
	if who := callerOf(r); who != nil && who.user != nil {
		return "user:" + who.user.Name
	}
	return "operator"
}
