package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/users"
)

// Reasons a change to the hub's users is refused, sent as "code"
// (ADR-0048).
const (
	codeBadName         = "bad_name"
	codeBadRole         = "bad_role"
	codeUserExists      = "user_exists"
	codeLastOwner       = "last_owner"
	codeConfirmPassword = "confirm_password"
)

// userView is a user as an owner manages them: never the password hash,
// which stays in the store.
type userView struct {
	Name               string `json:"name"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
	CreatedAt          int64  `json:"created_at"`
}

func viewOfUser(user *store.User) userView {
	return userView{Name: user.Name, Role: user.Role, MustChangePassword: user.MustChangePassword, CreatedAt: user.CreatedAt}
}

// oneTimeView answers an add or a reset. The one-time password is in this
// response and nowhere else: it is not stored, not logged, and not cached.
type oneTimeView struct {
	userView
	OneTimePassword string `json:"one_time_password"`
}

func writeOneTime(w http.ResponseWriter, status int, user *store.User, password string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, oneTimeView{userView: viewOfUser(user), OneTimePassword: password})
}

func (server *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	all, err := server.Store.Users().List(r.Context())
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views := make([]userView, 0, len(all))
	for _, user := range all {
		views = append(views, viewOfUser(user))
	}
	writeJSON(w, http.StatusOK, views)
}

func (server *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name            string `json:"name"`
		Role            string `json:"role"`
		CurrentPassword string `json:"current_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		server.jsonErr(w, http.StatusBadRequest, "bad json: %v", err)
		return
	}
	if in.Role == "" {
		in.Role = store.RoleMember
	}
	if !users.ValidRole(in.Role) {
		server.jsonErrCode(w, http.StatusBadRequest, codeBadRole, "%v", users.ErrBadRole)
		return
	}
	if grants(store.RoleMember, in.Role) && !server.confirmed(w, r, in.CurrentPassword) {
		return
	}
	password, err := server.Users.Add(r.Context(), in.Name, in.Role)
	if err != nil {
		server.userErr(w, err)
		return
	}
	user, err := server.Store.Users().GetByName(r.Context(), in.Name)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeOneTime(w, http.StatusCreated, user, password)
}

func (server *Server) handleResetUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	password, err := server.Users.Reset(r.Context(), name)
	if err != nil {
		server.userErr(w, err)
		return
	}
	user, err := server.Store.Users().GetByName(r.Context(), name)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeOneTime(w, http.StatusOK, user, password)
}

func (server *Server) handleSetUserRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role            string `json:"role"`
		CurrentPassword string `json:"current_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		server.jsonErr(w, http.StatusBadRequest, "bad json: %v", err)
		return
	}
	if !users.ValidRole(in.Role) {
		server.jsonErrCode(w, http.StatusBadRequest, codeBadRole, "%v", users.ErrBadRole)
		return
	}
	user, err := server.Store.Users().GetByName(r.Context(), r.PathValue("name"))
	if err != nil {
		server.userErr(w, err)
		return
	}
	if grants(user.Role, in.Role) && !server.confirmed(w, r, in.CurrentPassword) {
		return
	}
	if err := server.Users.SetRole(r.Context(), user.Name, in.Role); err != nil {
		server.userErr(w, err)
		return
	}
	user.Role = in.Role
	writeJSON(w, http.StatusOK, viewOfUser(user))
}

func (server *Server) handleRemoveUser(w http.ResponseWriter, r *http.Request) {
	if err := server.Users.Remove(r.Context(), r.PathValue("name")); err != nil {
		server.userErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// grants reports whether a change from role from to role to gives someone
// admin or owner they did not have, which asks the acting owner for their
// password again: a stolen session alone must not be able to make its
// thief an admin (ADR-0048).
func grants(from, to string) bool {
	return roleRank[to] > roleRank[from] && roleRank[to] >= roleRank[store.RoleAdmin]
}

// confirmed checks the password the caller typed again, and answers the
// request itself when it is missing or wrong. A user confirms with their
// password. The operator token has none, so a caller acting with it types
// the token. A missing one is refused without counting toward the lock: it
// is a client that did not ask, not a guess.
func (server *Server) confirmed(w http.ResponseWriter, r *http.Request, password string) bool {
	who := callerOf(r)
	if who.user == nil {
		if password == "" || subtle.ConstantTimeCompare([]byte(password), []byte(server.OperatorToken)) != 1 {
			server.jsonErrCode(w, http.StatusForbidden, codeConfirmPassword, "type the operator token again to grant admin or owner")
			return false
		}
		return true
	}
	if password == "" {
		server.jsonErrCode(w, http.StatusForbidden, codeConfirmPassword, "type your password again to grant admin or owner")
		return false
	}
	err := server.Users.Confirm(r.Context(), who.user, password)
	var throttled users.ThrottledError
	switch {
	case errors.As(err, &throttled):
		writeThrottled(w, throttled)
	case errors.Is(err, users.ErrBadCredentials):
		server.jsonErrCode(w, http.StatusForbidden, codeConfirmPassword, "type your password again to grant admin or owner")
	case err != nil:
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
	default:
		return true
	}
	return false
}

// userErr answers a refused change to the hub's users.
func (server *Server) userErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrBadName):
		server.jsonErrCode(w, http.StatusBadRequest, codeBadName, "%v", err)
	case errors.Is(err, users.ErrBadRole):
		server.jsonErrCode(w, http.StatusBadRequest, codeBadRole, "%v", err)
	case errors.Is(err, store.ErrDuplicate):
		server.jsonErrCode(w, http.StatusConflict, codeUserExists, "a user by that name already exists")
	case errors.Is(err, users.ErrLastOwner):
		server.jsonErrCode(w, http.StatusConflict, codeLastOwner, "%v", err)
	default:
		server.storeErr(w, err, "user")
	}
}
