package telegram

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTheHTTP1TransportDoesNotOfferH2 (#570): a server that speaks HTTP/2
// over TLS, as api.telegram.org does, picks h2 whenever the client offers
// it. The transport reads only HTTP/1.x, so an offer it cannot keep fails
// every call with a "malformed HTTP response": the server's SETTINGS frame.
func TestTheHTTP1TransportDoesNotOfferH2(t *testing.T) {
	// GODEBUG=http2client=0 hides the offer, and the fleet's hub runs with
	// it as the outage's workaround; the transport must not need it.
	t.Setenv("GODEBUG", "")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.EnableHTTP2 = true
	srv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}} // api.telegram.org's preference
	srv.StartTLS()
	defer srv.Close()

	transport := http1Transport(0)
	transport.TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	resp, err := (&http.Client{Transport: transport}).Get(srv.URL)
	if err != nil {
		t.Fatalf("a call through the HTTP/1 transport failed: %v", err)
	}
	resp.Body.Close()
	if resp.ProtoMajor != 1 || resp.TLS.NegotiatedProtocol != "http/1.1" {
		t.Fatalf("the HTTP/1 transport spoke %s, ALPN %q", resp.Proto, resp.TLS.NegotiatedProtocol)
	}
}
