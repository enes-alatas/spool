package httpapi

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/enes-alatas/spool/internal/operator"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/users"
)

// Reasons a sign-in or a password change is refused, sent as "code"
// (ADR-0048).
const (
	codeBadCredentials         = "bad_credentials"
	codeThrottled              = "throttled"
	codePasswordChangeRequired = "password_change_required"
	codePasswordTooShort       = "password_too_short"
	codePasswordTooLong        = "password_too_long"
	codePasswordReused         = "password_reused"
	codeNoUser                 = "no_user"
)

// meView is who a session belongs to. A token session has no name, acts
// as the owner, and is never due a change.
type meView struct {
	Name               string `json:"name"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
	Via                string `json:"via"`
}

func meOf(who *caller) meView {
	if who.user == nil {
		return meView{Role: store.RoleOwner, Via: "token"}
	}
	changeDue := who.user.MustChangePassword
	return meView{Name: who.user.Name, Role: who.user.Role, MustChangePassword: changeDue, Via: "password"}
}

// handleLogin trades a username and password, or for one more release the
// operator token, for a session cookie, and answers with who the session
// belongs to. It is the one route that may be called without a credential,
// since it is how one is obtained, and it is still behind the Host, origin
// and content-type checks.
func (server *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		server.jsonErr(w, http.StatusBadRequest, "bad json: %v", err)
		return
	}
	who := &caller{}
	var session string
	var err error
	if in.Username == "" && in.Token != "" {
		if !operator.Matches(server.OperatorToken, strings.TrimSpace(in.Token)) {
			server.jsonErrCode(w, http.StatusUnauthorized, "bad_operator_token", "that is not this hub's token")
			return
		}
		session, err = server.Users.StartTokenSession(r.Context(), server.OperatorToken)
	} else {
		who.user, err = server.Users.SignIn(r.Context(), strings.TrimSpace(in.Username), in.Password)
		var throttled users.ThrottledError
		switch {
		case errors.As(err, &throttled):
			writeThrottled(w, throttled)
			return
		case errors.Is(err, users.ErrBadCredentials):
			server.jsonErrCode(w, http.StatusUnauthorized, codeBadCredentials, "%v", err)
			return
		case err != nil:
			server.jsonErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		session, err = server.Users.StartSession(r.Context(), who.user.ID)
	}
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	setSessionCookie(w, r, session, 0)
	writeJSON(w, http.StatusOK, meOf(who))
}

// writeThrottled refuses a locked name: 429 throttled, with how long the
// lock still holds as retry_after and Retry-After, in whole seconds.
func writeThrottled(w http.ResponseWriter, throttled users.ThrottledError) {
	seconds := int(math.Ceil(throttled.RetryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error": throttled.Error(), "code": codeThrottled, "retry_after": seconds,
	})
}

// handleLogout ends the cookie's session and drops the cookie. It asks for
// no credential: a caller who can only reach this route can only end a
// session, and refusing to let someone sign out because they are not signed
// in helps nobody.
func (server *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookie); err == nil {
		if err := server.Users.EndSession(r.Context(), cookie.Value); err != nil {
			server.jsonErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// setSessionCookie sets the cookie to value, or clears it with a negative
// maxAge. HttpOnly, SameSite=Strict, and Secure when the request arrived
// over TLS (ADR-0030).
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   overTLS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (server *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, meOf(callerOf(r)))
}

// handleMePassword changes the session's user's password, and ends every
// other session they hold. While a change is due, the session has just
// proved the one-time password, so the current one is not asked for again.
func (server *Server) handleMePassword(w http.ResponseWriter, r *http.Request) {
	who := callerOf(r)
	if who.user == nil {
		server.jsonErrCode(w, http.StatusBadRequest, codeNoUser, "the operator token has no password to change")
		return
	}
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		server.jsonErr(w, http.StatusBadRequest, "bad json: %v", err)
		return
	}
	err := server.Users.ChangePassword(r.Context(), who.user, in.CurrentPassword, in.NewPassword, who.session)
	var throttled users.ThrottledError
	switch {
	case errors.As(err, &throttled):
		writeThrottled(w, throttled)
	case errors.Is(err, users.ErrBadCredentials):
		server.jsonErrCode(w, http.StatusUnauthorized, codeBadCredentials, "the current password is wrong")
	case errors.Is(err, users.ErrPasswordTooShort):
		server.jsonErrCode(w, http.StatusBadRequest, codePasswordTooShort, "%v", err)
	case errors.Is(err, users.ErrPasswordTooLong):
		server.jsonErrCode(w, http.StatusBadRequest, codePasswordTooLong, "%v", err)
	case errors.Is(err, users.ErrPasswordReused):
		server.jsonErrCode(w, http.StatusBadRequest, codePasswordReused, "%v", err)
	case err != nil:
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
	default:
		writeJSON(w, http.StatusOK, meOf(who))
	}
}
