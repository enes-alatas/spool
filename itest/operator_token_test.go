//go:build integration

package itest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The API is not open because it is on localhost (#239). This is the running
// binary, not a handler in a test: what another process on the machine, or a
// page in the operator's browser, actually gets back.
func TestAPIRefusesWhatIsNotTheOperator(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("guarded", nil)

	// A local process with no credential — the case the old API answered.
	resp, body := s.raw("GET", "/api/loops", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/loops with no token = %d, want 401 (%s)", resp.StatusCode, body)
	}
	if bytes.Contains(body, []byte("guarded")) {
		t.Error("a refused request was answered with the fleet anyway")
	}

	// A page on another site, posting a JSON body with a content type that
	// needs no preflight — with the session cookie attached, as a browser
	// would attach it.
	resp, _ = s.raw("POST", "/api/loops", strings.NewReader(`{"name":"evil"}`), map[string]string{
		"Content-Type": "text/plain;charset=UTF-8",
		"Origin":       "http://attacker.example",
		"Cookie":       "spool_operator=" + s.operatorToken,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST /api/loops = %d, want 403", resp.StatusCode)
	}

	// DNS rebinding: the request reaches the socket, and names a host this
	// hub does not answer to.
	resp, _ = s.raw("GET", "/api/loops", nil, map[string]string{
		"Host":          "spool.attacker.example",
		"Authorization": "Bearer " + s.operatorToken,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /api/loops with a foreign Host = %d, want 403", resp.StatusCode)
	}

	// The credential a loop actually holds, presented where the operator's
	// belongs. ADR-0030 calls two credentials that cannot be confused for one
	// another the point of the design; this is the test that a refactor
	// unifying the two checks would fail.
	resp, _ = s.raw("GET", "/api/loops", nil, map[string]string{
		"Authorization": "Bearer " + hubMCPToken(t, s, "guarded"),
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a loop's own MCP token at /api/loops = %d, want 401", resp.StatusCode)
	}

	// And the fleet is still there for the operator, who has the token.
	var loops []loopView
	s.mustJSON("GET", "/api/loops", nil, &loops)
	if len(loops) != 1 || loops[0].Name != "guarded" {
		t.Fatalf("the operator's own request must still work, got %+v", loops)
	}
}

// The control room's path: trade the token for a cookie once, then hold only
// the cookie — which is what an EventSource stream can carry and a header is
// not. Proved end to end against the running hub, because the cookie's
// attributes only mean anything to a real client.
func TestOperatorCookieLogin(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("cookied", nil)

	resp, body := s.raw("POST", "/api/login", strings.NewReader(`{"token":"wrong"}`),
		map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login with a wrong token = %d, want 401 (%s)", resp.StatusCode, body)
	}

	resp, body = s.raw("POST", "/api/login",
		strings.NewReader(`{"token":"`+s.operatorToken+`"}`),
		map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d, want 204 (%s)", resp.StatusCode, body)
	}
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "spool_operator" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("login set no session cookie")
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie = %+v, want HttpOnly and SameSite=Strict", session)
	}

	// The cookie alone, no Authorization header: the control room's requests.
	resp, body = s.raw("GET", "/api/loops", nil, map[string]string{
		"Cookie": session.Name + "=" + session.Value,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/loops with the session cookie = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var loops []loopView
	if err := json.Unmarshal(body, &loops); err != nil || len(loops) != 1 {
		t.Fatalf("cookie request returned %s (%v)", body, err)
	}

	// Logging out ends it: the same cookie value stops working once the
	// operator has dropped it... which it does not, because the cookie *is*
	// the token. Assert what is true rather than what would be nicer: logout
	// clears the browser's copy, and the token behind it is unchanged.
	resp, _ = s.raw("POST", "/api/logout", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("logout = %d, want 204", resp.StatusCode)
	}
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == "spool_operator" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout must expire the session cookie")
	}
}

// A method a route does not take is 405 with Allow, and a path no route has
// is 404, both as JSON (#245). Tier 2 runs a binary with no control room
// embedded, so this proves the answer on that build; the build that embeds
// one is proved at tier 1 (TestAPIRefusalsAreNeverTheUI).
func TestAPIMethodAndPathRefusals(t *testing.T) {
	s := startServer(t, t.TempDir())
	resp, body := s.do("DELETE", "/api/settings", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "GET, PUT" {
		t.Fatalf("DELETE /api/settings = %d, Allow %q: %s; want 405, Allow GET, PUT",
			resp.StatusCode, resp.Header.Get("Allow"), body)
	}
	resp, body = s.do("GET", "/api/no-such-route", nil)
	if resp.StatusCode != http.StatusNotFound || !json.Valid(body) {
		t.Fatalf("GET /api/no-such-route = %d %s, want a JSON 404", resp.StatusCode, body)
	}
}
