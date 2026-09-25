package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// guarded returns the guard wrapped around the session routes plus a stand-in
// for every other route, which records whether it was reached. Every refusal
// below has to stop short of that stand-in: a check that answers 403 *after*
// the route has already created a loop would read as a pass here and be no
// protection at all.
func guarded(t *testing.T, token string) (http.Handler, *bool) {
	t.Helper()
	return guardedOn(t, token, "127.0.0.1:8080")
}

// guardedOn is guarded with a say in where the hub believes it listens, which
// is what the Host and Origin checks are judged against.
func guardedOn(t *testing.T, token, listenAddr string) (http.Handler, *bool) {
	t.Helper()
	reached := false
	s := &Server{OperatorToken: token, ListenAddr: listenAddr}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	return s.guard(mux), &reached
}

// A real token's length, and obviously not one: the leading run is what
// tells the secret scanner this is a fixture (scripts/secret-rules.awk).
const testToken = "fixture-aaaa-0123456789abcdef0123456789abcdef0123456789abcdef"

func TestGuardRefusals(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		header map[string]string
		want   int
	}{
		{
			name: "no credential at all", method: "GET", path: "/api/loops",
			want: http.StatusUnauthorized,
		},
		{
			name: "a wrong token", method: "GET", path: "/api/loops",
			header: map[string]string{"Authorization": "Bearer not-the-token"},
			want:   http.StatusUnauthorized,
		},
		{
			name: "the right token in the wrong scheme", method: "GET", path: "/api/loops",
			header: map[string]string{"Authorization": "Basic " + testToken},
			want:   http.StatusUnauthorized,
		},
		{
			// the cross-origin POST the issue is about: simple content type,
			// no preflight, and in a browser it would carry the cookie
			name: "a mutation from another page", method: "POST", path: "/api/loops",
			body: `{"name":"evil"}`,
			header: map[string]string{
				"Origin":       "http://attacker.example",
				"Content-Type": "text/plain",
				"Cookie":       SessionCookie + "=" + testToken,
			},
			want: http.StatusForbidden,
		},
		{
			name: "a fetch that declares itself cross-site", method: "POST", path: "/api/loops",
			body: `{"name":"evil"}`,
			header: map[string]string{
				"Sec-Fetch-Site": "cross-site",
				"Content-Type":   "application/json",
				"Cookie":         SessionCookie + "=" + testToken,
			},
			want: http.StatusForbidden,
		},
		{
			// DNS rebinding: the packet reaches the socket, but the browser
			// tells us which name the page used
			name: "a Host this hub does not answer to", method: "GET", path: "/api/loops",
			header: map[string]string{"Host": "spool.attacker.example"},
			want:   http.StatusForbidden,
		},
		{
			name: "a Host on a port this hub does not listen on", method: "GET", path: "/api/loops",
			header: map[string]string{"Host": "127.0.0.1:9999"},
			want:   http.StatusForbidden,
		},
		{
			name: "a body that is not json", method: "POST", path: "/api/loops",
			body: `{"name":"evil"}`,
			header: map[string]string{
				"Content-Type":  "application/x-www-form-urlencoded",
				"Authorization": "Bearer " + testToken,
			},
			want: http.StatusUnsupportedMediaType,
		},
		{
			name: "a file:// page's origin", method: "POST", path: "/api/loops",
			body: `{"name":"evil"}`,
			header: map[string]string{
				"Origin":        "null",
				"Content-Type":  "application/json",
				"Authorization": "Bearer " + testToken,
			},
			want: http.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, reached := guarded(t, testToken)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Host = "127.0.0.1:8080"
			for k, v := range tc.header {
				if k == "Host" {
					req.Host = v
					continue
				}
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			if *reached {
				t.Error("the request reached the route — a refusal after the work is not a refusal")
			}
		})
	}
}

func TestGuardAdmits(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		header map[string]string
	}{
		{
			name: "health, before there is a token to present", method: "GET", path: "/api/health",
		},
		{
			name: "version, for a bug report", method: "GET", path: "/api/version",
		},
		{
			name: "the token in a header", method: "GET", path: "/api/loops",
			header: map[string]string{"Authorization": "Bearer " + testToken},
		},
		{
			name: "the cookie the control room holds", method: "GET", path: "/api/loops",
			header: map[string]string{"Cookie": SessionCookie + "=" + testToken},
		},
		{
			name: "a same-origin mutation from the control room", method: "POST", path: "/api/loops",
			body: `{"name":"greeter"}`,
			header: map[string]string{
				"Origin":         "http://127.0.0.1:8080",
				"Sec-Fetch-Site": "same-origin",
				"Content-Type":   "application/json",
				"Cookie":         SessionCookie + "=" + testToken,
			},
		},
		{
			name: "a content type with a charset", method: "POST", path: "/api/loops",
			body: `{"name":"greeter"}`,
			header: map[string]string{
				"Content-Type":  "application/json; charset=utf-8",
				"Authorization": "Bearer " + testToken,
			},
		},
		{
			name: "localhost by name", method: "GET", path: "/api/loops",
			header: map[string]string{
				"Host":          "localhost:8080",
				"Authorization": "Bearer " + testToken,
			},
		},
		{
			// a bodiless POST is how pause, resume, wake and kill are called
			name: "a POST with no body at all", method: "POST", path: "/api/loops/greeter/pause",
			header: map[string]string{"Authorization": "Bearer " + testToken},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, reached := guarded(t, testToken)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Host = "127.0.0.1:8080"
			for k, v := range tc.header {
				if k == "Host" {
					req.Host = v
					continue
				}
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK || !*reached {
				t.Errorf("status = %d, reached = %v; want 200 and the route reached (body %s)",
					w.Code, *reached, w.Body.String())
			}
		})
	}
}

// The control room trades the token for a cookie once, and the cookie is
// unreachable from script: a credential a page can read is a credential the
// next injected script exfiltrates.
func TestLoginIssuesAnHTTPOnlyCookie(t *testing.T) {
	h, _ := guarded(t, testToken)

	req := httptest.NewRequest("POST", loginPath, strings.NewReader(`{"token":"`+testToken+`"}`))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("login = %d, want 204 (%s)", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login set %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	switch {
	case c.Name != SessionCookie:
		t.Errorf("cookie name = %q, want %q", c.Name, SessionCookie)
	case !c.HttpOnly:
		t.Error("the session cookie must be HttpOnly — script must not be able to read it")
	case c.SameSite != http.SameSiteStrictMode:
		t.Error("the session cookie must be SameSite=Strict — it must not ride a cross-site request")
	}

	// And the wrong token buys nothing.
	bad := httptest.NewRequest("POST", loginPath, strings.NewReader(`{"token":"nope"}`))
	bad.Host = "127.0.0.1:8080"
	bad.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, bad)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("login with a wrong token = %d, want 401", w.Code)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Errorf("a refused login set %d cookies, want none", len(got))
	}
}

// A refusal is JSON like every other error the API returns, so the control
// room can tell "log in again" from "that failed" without parsing prose.
func TestUnauthorizedIsMachineReadable(t *testing.T) {
	h, _ := guarded(t, testToken)
	req := httptest.NewRequest("GET", "/api/loops", nil)
	req.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("refusal body is not json: %v (%s)", err, body)
	}
	if out["code"] != "no_operator_token" {
		t.Errorf("refusal code = %q, want no_operator_token", out["code"])
	}
}

// The control room's own assets are not behind the credential: the page that
// asks for the token has to load before anyone has presented one.
func TestGuardLeavesTheUIAlone(t *testing.T) {
	for _, path := range []string{"/", "/assets/index.js", "/loops/greeter"} {
		h, reached := guarded(t, testToken)
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "127.0.0.1:8080"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if !*reached {
			t.Errorf("GET %s was refused; the login page must be servable", path)
		}
	}
}

// A hub on a wildcard address is reachable by names it cannot enumerate, so
// it takes any literal IP as a Host — and still refuses a name, which is what
// a rebinding attack has to supply (ADR-0030).
func TestWildcardListenerTakesAddressesNotNames(t *testing.T) {
	cases := []struct {
		host string
		want int
	}{
		{host: "127.0.0.1:8080", want: http.StatusOK},
		{host: "192.168.1.20:8080", want: http.StatusOK},
		{host: "[::1]:8080", want: http.StatusOK},
		{host: "localhost:8080", want: http.StatusOK},
		// the rebinding move: a name the attacker controls, pointed at us
		{host: "spool.attacker.example:8080", want: http.StatusForbidden},
		{host: "spool.attacker.example", want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			h, _ := guardedOn(t, testToken, "0.0.0.0:8080")
			req := httptest.NewRequest("GET", "/api/loops", nil)
			req.Host = tc.host
			req.Header.Set("Authorization", "Bearer "+testToken)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("Host %q = %d, want %d", tc.host, w.Code, tc.want)
			}
		})
	}
}

// The posture ADR-0030 blesses: a hub behind a TLS proxy, reached as that
// proxy's name. The operator names it; nothing else is guessed.
func TestTrustedHostAdmitsTheProxysName(t *testing.T) {
	newHub := func() (http.Handler, *bool) {
		reached := false
		s := &Server{
			OperatorToken: testToken,
			ListenAddr:    "127.0.0.1:8080",
			TrustedHosts:  []string{"spool.example.com"},
		}
		mux := http.NewServeMux()
		mux.HandleFunc("POST /api/login", s.handleLogin)
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		})
		return s.guard(mux), &reached
	}

	cases := []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{name: "the proxy's name, no port", host: "spool.example.com", want: http.StatusOK},
		{name: "the proxy's name with a port", host: "spool.example.com:443", want: http.StatusOK},
		{name: "its own origin", host: "spool.example.com", origin: "https://spool.example.com", want: http.StatusOK},
		{name: "loopback still works", host: "127.0.0.1:8080", want: http.StatusOK},
		{name: "a name nobody trusted", host: "spool.attacker.example", want: http.StatusForbidden},
		{
			name: "the trusted host as an attacker's origin", host: "spool.example.com",
			origin: "https://attacker.example", want: http.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHub()
			req := httptest.NewRequest("GET", "/api/loops", nil)
			req.Host = tc.host
			req.Header.Set("Authorization", "Bearer "+testToken)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("Host %q Origin %q = %d, want %d", tc.host, tc.origin, w.Code, tc.want)
			}
		})
	}
}

// The cookie is the token, so a session established over TLS must not be
// sent back in the clear. It cannot be unconditional: a hub on a plain-http
// LAN address would never receive the cookie again.
func TestSessionCookieIsSecureOverTLS(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proto string
		want  bool
	}{
		{name: "plain http", proto: "", want: false},
		{name: "behind a TLS proxy", proto: "https", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := guarded(t, testToken)
			req := httptest.NewRequest("POST", loginPath, strings.NewReader(`{"token":"`+testToken+`"}`))
			req.Host = "127.0.0.1:8080"
			req.Header.Set("Content-Type", "application/json")
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("login set %d cookies, want 1", len(cookies))
			}
			if cookies[0].Secure != tc.want {
				t.Errorf("cookie Secure = %v, want %v", cookies[0].Secure, tc.want)
			}
		})
	}
}

// The two routes that need no credential are the two where a method mismatch
// is reachable by anyone. The guard lets them past the token check and
// nothing more; apiFallback refuses the wrong method, as on any route (#245).
// This lives at tier 1 because only here is the control room's catch-all
// behind the fallback, as it is in the shipped binary: `make server` skips
// vite, so tier 2 has none.
func TestSessionRoutesRefuseTheWrongMethod(t *testing.T) {
	for _, path := range []string{loginPath, logoutPath} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			t.Run(method+" "+path, func(t *testing.T) {
				rec := httptest.NewRecorder()
				shipped(t).ServeHTTP(rec, httptest.NewRequest(method, "http://127.0.0.1:8080"+path, nil))
				if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
					t.Errorf("%s %s = %d, Allow %q; want 405, Allow POST", method, path, rec.Code, rec.Header().Get("Allow"))
				}
			})
		}
	}
}

// shipped is the whole API as the shipped binary serves it: the real route
// list, with an embedded control room, and so the UI's catch-all behind
// every route. A build without the UI registers no catch-all, which is why
// tier 2 (make itest builds without vite) cannot see what this sees (#245).
func shipped(t *testing.T) http.Handler {
	t.Helper()
	s := &Server{OperatorToken: testToken, ListenAddr: "127.0.0.1:8080",
		WebFS: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}}
	return s.Handler()
}

// Under /api/ the answer is always JSON: a method the path does not take is
// 405 naming the ones it does, a path no route has is 404, and the control
// room's index.html is never the answer, with a 200 that says it worked.
func TestAPIRefusalsAreNeverTheUI(t *testing.T) {
	cases := []struct {
		method, path string
		code         int
		allow        string
	}{
		{"DELETE", "/api/settings", 405, "GET, PUT"},
		{"GET", "/api/loops/greeter/wake", 405, "POST"},
		{"POST", "/api/loops/greeter", 405, "GET, PATCH, DELETE"},
		{"GET", "/api/no-such-route", 404, ""},
		{"POST", "/api/loops/greeter/no-such-action", 404, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://127.0.0.1:8080"+tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+testToken)
			rec := httptest.NewRecorder()
			shipped(t).ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.code, rec.Body)
			}
			if got := rec.Header().Get("Allow"); got != tc.allow {
				t.Errorf("Allow = %q, want %q", got, tc.allow)
			}
			var body struct{ Error string }
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Errorf("body is not the JSON error shape: %q", rec.Body)
			}
		})
	}
	// And the UI is still the UI.
	rec := httptest.NewRecorder()
	shipped(t).ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080/loops/greeter", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Errorf("GET /loops/greeter = %d %q, want the control room", rec.Code, rec.Body)
	}
}

// A trusted entry without a port matches whatever port the request names —
// and "without a port" cannot be read off the presence of a colon, because
// an IPv6 literal is all colons. Judged by counting them, `[2001:db8::1]`
// would take the with-a-port branch and silently refuse the very requests
// a proxy on 8443 sends.
func TestTrustedIPv6LiteralIsNotMistakenForAPort(t *testing.T) {
	for _, host := range []string{"[2001:db8::1]", "[2001:db8::1]:8443"} {
		t.Run(host, func(t *testing.T) {
			s := &Server{
				OperatorToken: testToken,
				ListenAddr:    "127.0.0.1:8080",
				TrustedHosts:  []string{"[2001:db8::1]"},
			}
			if !s.hostAllowed(host) {
				t.Errorf("hostAllowed(%q) = false, want true", host)
			}
		})
	}
}
