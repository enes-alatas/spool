package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// fixedListener is a cloud listener that is open on addr, or off for "".
type fixedListener struct{ addr string }

func (listener fixedListener) Listening() (string, bool) { return listener.addr, false }
func (fixedListener) Open(string) (string, error)        { return "", nil }
func (fixedListener) Close() error                       { return nil }

// cloudCheckServer is a hub over a fresh store whose public address is
// address, with its cloud listener open.
func cloudCheckServer(t *testing.T, address string) *Server {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Settings().Set(context.Background(), store.SettingCloudAddress, address); err != nil {
		t.Fatal(err)
	}
	return &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CloudListener: fixedListener{"127.0.0.1:8090"}}
}

// checked runs the check and returns what it recorded.
func checked(t *testing.T, server *Server, address string) CloudCheckResult {
	t.Helper()
	server.CheckCloudAddress(context.Background())
	result := server.cloudCheck.result(address)
	if result == nil {
		t.Fatal("the check recorded nothing")
	}
	return *result
}

// The check passes only when the request through the address comes back
// to this hub's cloud listener, and otherwise names where it stopped.
func TestTheCloudCheckNamesWhereItStopped(t *testing.T) {
	t.Run("this hub answers", func(t *testing.T) {
		server := cloudCheckServer(t, "")
		public := httptest.NewServer(server.CloudHandler())
		t.Cleanup(public.Close)
		server = withAddress(t, server, public.URL)
		if result := checked(t, server, public.URL); result.State != CloudCheckOK || result.CheckedAt == 0 {
			t.Fatalf("result = %+v, want ok", result)
		}
	})
	t.Run("another server answers", func(t *testing.T) {
		other := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(other.Close)
		server := cloudCheckServer(t, other.URL)
		if result := checked(t, server, other.URL); result.Stage != CloudStageOtherServer || !strings.Contains(result.Detail, "404") {
			t.Fatalf("result = %+v, want another server, naming its answer", result)
		}
	})
	t.Run("a redirect is not followed", func(t *testing.T) {
		server := cloudCheckServer(t, "")
		hub := httptest.NewServer(server.CloudHandler())
		t.Cleanup(hub.Close)
		redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, hub.URL+r.URL.Path, http.StatusFound)
		}))
		t.Cleanup(redirecting.Close)
		server = withAddress(t, server, redirecting.URL)
		if result := checked(t, server, redirecting.URL); result.Stage != CloudStageOtherServer {
			t.Fatalf("result = %+v, want another server: the token would follow the redirect too", result)
		}
	})
	t.Run("a certificate this machine doesn't trust", func(t *testing.T) {
		server := cloudCheckServer(t, "")
		public := httptest.NewTLSServer(server.CloudHandler())
		t.Cleanup(public.Close)
		server = withAddress(t, server, public.URL)
		if result := checked(t, server, public.URL); result.Stage != CloudStageTLS {
			t.Fatalf("result = %+v, want TLS", result)
		}
	})
	t.Run("nothing listening", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := "http://" + listener.Addr().String()
		_ = listener.Close()
		server := cloudCheckServer(t, address)
		if result := checked(t, server, address); result.Stage != CloudStageUnreachable {
			t.Fatalf("result = %+v, want unreachable", result)
		}
	})
	t.Run("the listener is off", func(t *testing.T) {
		server := cloudCheckServer(t, "https://hub.example.test")
		server.CloudListener = fixedListener{}
		if result := checked(t, server, "https://hub.example.test"); result.Stage != CloudStageListenerOff {
			t.Fatalf("result = %+v, want the listener off, with no request made", result)
		}
	})
}

// withAddress stores address as server's public address.
func withAddress(t *testing.T, server *Server, address string) *Server {
	t.Helper()
	if err := server.Store.Settings().Set(context.Background(), store.SettingCloudAddress, address); err != nil {
		t.Fatal(err)
	}
	return server
}

// A failed request is put down to the stage it stopped at, and the
// detail leaves out the URL, which carries the check's value.
func TestCloudCheckFailureStages(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.DNSError{Err: "no such host", Name: "hub.example.test", IsNotFound: true}, CloudStageDNS},
		{context.DeadlineExceeded, CloudStageTimeout},
		{&net.OpError{Op: "dial", Err: &timeoutErr{}}, CloudStageTimeout},
		{&net.OpError{Op: "dial", Err: io.EOF}, CloudStageUnreachable},
	} {
		stage, detail := cloudCheckFailure(&url.Error{Op: "Get", URL: "https://hub.example.test/mcp/check/secret-check-value", Err: tc.err})
		if stage != tc.want {
			t.Errorf("%v: stage %q, want %q", tc.err, stage, tc.want)
		}
		if strings.Contains(detail, "secret-check-value") {
			t.Errorf("%v: detail %q carries the URL", tc.err, detail)
		}
	}
}

type timeoutErr struct{}

func (*timeoutErr) Error() string   { return "i/o timeout" }
func (*timeoutErr) Timeout() bool   { return true }
func (*timeoutErr) Temporary() bool { return true }

// The address is stored as an origin: HTTPS, no path, query or user. Plain
// HTTP on loopback passes only under the tests' opt-in: no cloud session
// reaches it, though the hub would check it green from itself. A quick
// tunnel's address is accepted with its warning.
func TestCloudAddressOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		" https://Hub.Example.test/ ":         "https://hub.example.test",
		"https://hub.example.test:8443":       "https://hub.example.test:8443",
		"https://quiet-owl.trycloudflare.com": "https://quiet-owl.trycloudflare.com",
	} {
		if got, err := CloudAddressOrigin(raw, false); err != nil || got != want {
			t.Errorf("CloudAddressOrigin(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"", "hub.example.test", "http://hub.example.test", "https://hub.example.test/mcp",
		"https://user@hub.example.test", "https://hub.example.test/?a=b", "ftp://hub.example.test",
		"http://127.0.0.1:8090", "http://localhost:8090",
	} {
		if got, err := CloudAddressOrigin(raw, false); err == nil {
			t.Errorf("CloudAddressOrigin(%q) = %q, want it refused", raw, got)
		}
	}
	if got, err := CloudAddressOrigin("http://127.0.0.1:8090/", true); err != nil || got != "http://127.0.0.1:8090" {
		t.Errorf("under the opt-in, loopback = %q, %v", got, err)
	}
	if got, err := CloudAddressOrigin("http://hub.example.test", true); err == nil {
		t.Errorf("under the opt-in, plain HTTP off loopback = %q, want it refused", got)
	}
	if got := cloudAddressWarning("https://quiet-owl.trycloudflare.com"); got != cloudWarningQuickTunnel {
		t.Errorf("a quick tunnel's warning = %q", got)
	}
	for _, address := range []string{"https://hub.example.test", "https://hub.tail1234.ts.net", "https://trycloudflare.com.example.test"} {
		if got := cloudAddressWarning(address); got != "" {
			t.Errorf("%s warned %q", address, got)
		}
	}
}
