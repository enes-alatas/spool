package httpapi

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/enes-alatas/spool/internal/operator"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/users"
)

// The API's trust model (#239). Reaching the port is not evidence of
// anything: another local account has the same reach, and so does a page in
// the operator's own browser, which can issue a cross-origin request at
// 127.0.0.1 without a preflight if it keeps the request "simple". A request
// is the operator's when it carries a credential and did not arrive from
// somewhere else's page. Three checks, each of which alone is enough to
// refuse:
//
//   - the Host it was addressed to is one this hub answers to, which is what
//     a DNS rebinding attack cannot arrange;
//   - it is not cross-site, by Origin or by Sec-Fetch-Site;
//   - it carries a credential: the operator token in an Authorization
//     header, or the cookie the login route sets for the control room,
//     which names a user's session (ADR-0048).
//
// A body is additionally required to be JSON, so the content types a
// cross-origin form can send without a preflight never reach a decoder.
const (
	// SessionCookie carries the control room's session ID: the control room
	// cannot set a header on an EventSource stream and should not hold a
	// credential in reachable storage. HttpOnly keeps it out of script,
	// SameSite=Strict keeps it off any request another site originates.
	SessionCookie = "spool_operator"

	loginPath      = "/api/login"
	logoutPath     = "/api/logout"
	mePath         = "/api/me"
	mePasswordPath = "/api/me/password"
)

// sessionPath names the two routes that establish and end a session rather
// than use one. They are still behind the Host, origin and content-type
// checks above — only the credential is what they do not require.
func sessionPath(path string) bool { return path == loginPath || path == logoutPath }

// openPaths answer without a credential: a supervisor restarting the hub and
// an operator reporting a bug both need them before they have a token, and
// neither names anything a loop or a stranger does not already know. Keeping
// that true is on whoever adds a field to either: health says only that the
// hub is up, and what the host runs is served behind the credential (#258).
func openPath(path string) bool {
	return path == "/api/health" || path == "/api/version"
}

// guard is the middleware every /api route is served behind. Paths outside
// /api — the control room's own assets — are left alone: the login page has
// to load before there is anything to authenticate with.
func (server *Server) guard(next *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if !server.hostAllowed(r.Host) {
			server.jsonErr(w, http.StatusForbidden, "unexpected Host %q", r.Host)
			return
		}
		if openPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			server.jsonErr(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !server.originAllowed(origin) {
			server.jsonErr(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if r.ContentLength != 0 && !isJSON(r.Header.Get("Content-Type")) && !isUpload(r) {
			server.jsonErr(w, http.StatusUnsupportedMediaType, "this route takes application/json")
			return
		}
		// The session routes are registered on the mux like everything else,
		// so the route list stays the route list; what they skip is the
		// token check, because obtaining the token is what one of them is
		// for and the other asks for nothing. A wrong method on them gets
		// apiFallback's 405, as on any route (#245).
		if sessionPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		caller, err := server.identify(r)
		if err != nil {
			server.jsonErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		if caller == nil {
			server.jsonErrCode(w, http.StatusUnauthorized, "no_operator_token",
				"this route needs a signed-in session or the operator token — run `spool token` to print it")
			return
		}
		// The hub holds a user to their one-time password's change itself,
		// rather than trusting the control room to show nothing else.
		if caller.user != nil && caller.user.MustChangePassword && !changeExempt(r.URL.Path) {
			server.jsonErrCode(w, http.StatusForbidden, codePasswordChangeRequired,
				"change the one-time password first")
			return
		}
		if !allows(next, r, caller) {
			server.jsonErrCode(w, http.StatusForbidden, codeForbiddenRole,
				"a %s may not do this", roleOf(caller))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

// changeExempt names the routes a user whose password change is due may
// still use: who they are, the change itself, and signing out.
func changeExempt(path string) bool {
	return path == mePath || path == mePasswordPath || path == logoutPath
}

// caller is who a request comes from (ADR-0048): a user, by the session
// their cookie names, or the operator token, which acts as the owner and
// has no user. session is the cookie's session ID, "" for a bearer.
type caller struct {
	user    *store.User
	session string
}

type callerKey struct{}

// callerOf is the caller the guard admitted the request as.
func callerOf(r *http.Request) *caller {
	who, _ := r.Context().Value(callerKey{}).(*caller)
	return who
}

// identify reads the credential from wherever the caller put it, nil when
// there is none or it is no good. A script or a curl sends the token as a
// bearer. The control room has the cookie, which names a session or, from
// before sessions had rows, holds the token itself: that cookie keeps
// working for as long as the token signs in (ADR-0048).
func (server *Server) identify(r *http.Request) (*caller, error) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if ok && operator.Matches(server.OperatorToken, strings.TrimSpace(token)) {
			return &caller{}, nil
		}
		return nil, nil
	}
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, nil
	}
	if operator.Matches(server.OperatorToken, cookie.Value) {
		return &caller{}, nil
	}
	_, user, err := server.Users.Session(r.Context(), cookie.Value, server.OperatorToken)
	if errors.Is(err, users.ErrSessionNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &caller{user: user, session: cookie.Value}, nil
}

// hostAllowed answers whether a request was addressed to this hub by a name
// it recognises. A browser sends the name the page used, so a page served
// from a name that resolves to 127.0.0.1 — the DNS rebinding move — arrives
// with a Host this never matches, even though the packet reached the socket.
//
// The listener's own address always passes, plus the loopback spellings a
// person types, plus whatever the operator named with --trusted-host: a hub
// behind a TLS proxy is reached as that proxy's name, which this cannot
// guess and must not accept blindly (ADR-0030). A hub on a wildcard address
// also accepts any Host that is a literal IP: the operator asked for every
// interface, and a rebinding attack needs a *name*.
func (server *Server) hostAllowed(host string) bool {
	if host == "" {
		return false
	}
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		name, port = host, ""
	}
	for _, trusted := range server.TrustedHosts {
		// An entry is a name, optionally with a port. With a port it has to
		// match whole; without one it matches the name whatever port the
		// request named, because a proxy on 443 sends no port at all and an
		// operator should not have to write both spellings.
		if strings.EqualFold(host, trusted) {
			return true
		}
		// An error means the entry named no port — SplitHostPort is what
		// tells a port from an IPv6 literal, which counting colons cannot.
		// Its brackets come off for the comparison, because splitting the
		// request's Host took them off there.
		if _, _, err := net.SplitHostPort(trusted); err != nil {
			if strings.EqualFold(name, strings.Trim(trusted, "[]")) {
				return true
			}
		}
	}
	if _, listenPort, err := net.SplitHostPort(server.ListenAddr); err == nil {
		// A port the hub does not listen on is somebody's proxy, and unless
		// the operator named it above, the Origin behind it is not one this
		// can vouch for.
		if port != "" && port != listenPort {
			return false
		}
	}
	switch strings.ToLower(name) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	if listenHost, _, err := net.SplitHostPort(server.ListenAddr); err == nil {
		if strings.EqualFold(name, listenHost) {
			return true
		}
		if isWildcardHost(listenHost) && net.ParseIP(strings.Trim(name, "[]")) != nil {
			return true
		}
	}
	return false
}

// originAllowed answers whether a page that originated this request is the
// control room itself. Same host rules as above, and the scheme has to be
// http or https so an extension or a file:// page ("null") is not an origin
// this trusts.
func (server *Server) originAllowed(origin string) bool {
	scheme, rest, ok := strings.Cut(origin, "://")
	if !ok || (scheme != "http" && scheme != "https") {
		return false
	}
	return server.hostAllowed(rest)
}

func isWildcardHost(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

// isJSON accepts the body types the API decodes, ignoring parameters so a
// charset does not make a well-formed request fail. Everything else — the
// form and text types a cross-origin POST can send without a preflight —
// stops here rather than at a decoder, but for the one upload isUpload
// admits.
func isJSON(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mt == "application/json"
}

// isUpload is the one exception to JSON bodies (ADR-0030, amended for
// #460): the composer's file, raw, to the upload route. A cross-site form
// cannot send application/octet-stream without a preflight, so it takes no
// shape off the table that JSON put there.
func isUpload(r *http.Request) bool {
	return isOctetStream(r.Header.Get("Content-Type")) && r.Method == http.MethodPost && r.URL.Path == uploadPath
}

func isOctetStream(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == "application/octet-stream"
}

// overTLS reports whether the request reached us encrypted, directly or
// through a proxy that says so. SameSite=Strict does nothing about a
// same-site request that is merely unencrypted, so a session established
// over TLS is marked Secure and will not be sent back in the clear
// (ADR-0030). It cannot be unconditional: a hub on a plain-http LAN address
// is a supported posture and would never receive the cookie again.
//
// Believing X-Forwarded-Proto can only make the cookie stricter, never
// laxer, so a caller who sets it is only tightening their own session.
func overTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
