package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// The hub's Claude logins (ADR-0060). Any signed-in user may add one,
// which makes them its owner; its owner or an admin signs it in, checks
// it, signs it out and removes it. The routes are a member's, and each
// handler that changes a login asks whether the caller may.

// codeClaudeLogin is the reason a Claude login route is refused.
const codeClaudeLogin = "claude_login"

// claudeLoginView is a Claude login as the API shows it: never its tokens.
type claudeLoginView struct {
	Name string `json:"name"`
	// Email is the address it signed in with, "" when unknown, or when the
	// caller is neither its owner nor an admin.
	Email string `json:"email,omitempty"`
	// Owner is the name of the user who added it, "" for one the hub made.
	Owner     string `json:"owner,omitempty"`
	SignedIn  bool   `json:"signed_in"`
	Default   bool   `json:"default"`
	CreatedAt int64  `json:"created_at"`
	// Check is its last login check, absent before the first.
	Check *store.LoginCheckRecord `json:"check,omitempty"`
}

type claudeLoginsView struct {
	Logins []claudeLoginView `json:"logins"`
}

type addClaudeLoginReq struct {
	Name string `json:"name"`
}

func (server *Server) handleListClaudeLogins(w http.ResponseWriter, r *http.Request) {
	logins, err := server.Store.ClaudeLogins().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	view := claudeLoginsView{Logins: []claudeLoginView{}}
	seenDefault := false
	for _, login := range logins {
		seenDefault = seenDefault || login.Name == store.DefaultClaudeLogin
		entry, err := server.claudeLoginView(r, login)
		if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		view.Logins = append(view.Logins, entry)
	}
	if !seenDefault {
		// the default is there to sign in even before anyone has
		entry, err := server.claudeLoginView(r, &store.ClaudeLogin{Name: store.DefaultClaudeLogin})
		if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		view.Logins = append([]claudeLoginView{entry}, view.Logins...)
	}
	writeJSON(w, 200, view)
}

// claudeLoginView is login as the caller of r sees it: the address it
// signed in with only when they may manage it, so a member sees no one
// else's.
func (server *Server) claudeLoginView(r *http.Request, login *store.ClaudeLogin) (claudeLoginView, error) {
	ctx := r.Context()
	view := claudeLoginView{
		Name: login.Name, SignedIn: login.SignedIn(),
		Default: login.Name == store.DefaultClaudeLogin, CreatedAt: login.CreatedAt,
		Check: loop.SettledCheck(login.Check),
	}
	if mayManage(callerOf(r), login) {
		view.Email = login.Account.Email
	}
	if view.Default {
		check, err := loop.LastLoginCheck(ctx, server.Store.Settings())
		if err != nil {
			return view, err
		}
		if view.Check = nil; check.At != 0 {
			view.Check = &check
		}
	}
	if login.OwnerUserID != "" {
		owner, err := server.Store.Users().Get(ctx, login.OwnerUserID)
		switch {
		case err == nil:
			view.Owner = owner.Name
		case !errors.Is(err, store.ErrNotFound):
			return view, err
		}
	}
	return view, nil
}

// handleAddClaudeLogin adds a login, signed out, owned by the caller. Its
// sign-in comes next, under the name.
func (server *Server) handleAddClaudeLogin(w http.ResponseWriter, r *http.Request) {
	var req addClaudeLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if !nameRe.MatchString(req.Name) {
		server.jsonErrCode(w, 400, codeClaudeLogin, "name must match %s", nameRe.String())
		return
	}
	if req.Name == store.DefaultClaudeLogin {
		// the hub's own, there before anyone adds it, and an admin's to sign in
		server.jsonErrCode(w, 409, codeClaudeLogin, "the default Claude login is the hub's: sign it in rather than adding it")
		return
	}
	login := &store.ClaudeLogin{Name: req.Name, CreatedAt: time.Now().UnixMilli()}
	if who := callerOf(r); who != nil && who.user != nil {
		login.OwnerUserID = who.user.ID
	}
	switch err := server.Store.ClaudeLogins().Create(r.Context(), login); {
	case errors.Is(err, store.ErrDuplicate):
		server.jsonErrCode(w, 409, codeClaudeLogin, "the hub already holds a Claude login named %s", req.Name)
		return
	case err != nil:
		server.jsonErr(w, 500, "%v", err)
		return
	}
	view, err := server.claudeLoginView(r, login)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 201, view)
}

// managedLogin reads the login the route names and answers whether the
// caller may change it: its owner, or an admin. The default is there even
// before it is stored, owned by no one. It writes the refusal itself, and
// returns nil after one.
func (server *Server) managedLogin(w http.ResponseWriter, r *http.Request) *store.ClaudeLogin {
	name := r.PathValue("name")
	login, err := server.Store.ClaudeLogins().Get(r.Context(), name)
	switch {
	case errors.Is(err, store.ErrNotFound) && name == store.DefaultClaudeLogin:
		login = &store.ClaudeLogin{Name: name}
	case errors.Is(err, store.ErrNotFound):
		server.jsonErrCode(w, 404, codeClaudeLogin, "the hub holds no Claude login named %s", name)
		return nil
	case err != nil:
		server.jsonErr(w, 500, "%v", err)
		return nil
	}
	if mayManage(callerOf(r), login) {
		return login
	}
	server.jsonErrCode(w, 403, codeForbiddenRole, "only its owner or an admin may change the Claude login %s", name)
	return nil
}

// mayManage reports whether who may change login: its owner, or an admin.
// A request the guard admitted no one for may not, so a route the guard
// missed is closed, not open.
func mayManage(who *caller, login *store.ClaudeLogin) bool {
	return who != nil && (roleRank[roleOf(who)] >= roleRank[store.RoleAdmin] ||
		(who.user != nil && login.OwnerUserID != "" && who.user.ID == login.OwnerUserID))
}

func (server *Server) handleStartClaudeLoginSignIn(w http.ResponseWriter, r *http.Request) {
	login := server.managedLogin(w, r)
	if login == nil {
		return
	}
	if server.CloudSignIn == nil {
		server.jsonErrCode(w, 409, codeCloudSignIn, "this hub can't sign in to a Claude account")
		return
	}
	server.startSignIn(w, r, login.Name)
}

// handleFinishClaudeLoginSignIn hands the CLI the code, stores what it
// signed in as, sealed, and checks it.
func (server *Server) handleFinishClaudeLoginSignIn(w http.ResponseWriter, r *http.Request) {
	login := server.managedLogin(w, r)
	if login == nil {
		return
	}
	if server.CloudSignIn == nil {
		server.jsonErrCode(w, 409, codeCloudSignIn, "this hub can't sign in to a Claude account")
		return
	}
	var req cloudSignInCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if !signInCodeShaped(req.Code) {
		server.jsonErrCode(w, 400, codeCloudSignIn, "that is not a sign-in code: one word, at most %d bytes", maxSignInCode)
		return
	}
	account, err := server.CloudSignIn.Finish(r.Context(), login.Name, req.Code)
	switch {
	case errors.Is(err, ErrNoCloudSignIn):
		server.jsonErrCode(w, 409, codeCloudSignIn, "%v", err)
		return
	case errors.Is(err, ErrCloudSignInRefused):
		server.jsonErrCode(w, 400, codeCloudSignIn, "%v", err)
		return
	case err != nil:
		server.Log.Warn("Claude login sign-in didn't finish", "login", login.Name, "err", err)
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.changeLogin(w, r, login.Name, account)
}

// handleSignOutClaudeLogin signs the login out, revoking it at the source,
// and keeps it, so the loops on it keep a valid name. It is never refused:
// the loops on it are held until it is signed in again or they move
// (ADR-0060).
func (server *Server) handleSignOutClaudeLogin(w http.ResponseWriter, r *http.Request) {
	if login := server.managedLogin(w, r); login != nil {
		server.changeLogin(w, r, login.Name, store.CloudAccount{})
	}
}

// changeLogin stores the login's sign-in, the zero account to sign it out,
// tells the hub, and answers with the login. A closed tab doesn't cut the
// change short, as for changeCloudAccount.
func (server *Server) changeLogin(w http.ResponseWriter, r *http.Request, name string, account store.CloudAccount) {
	ctx := context.WithoutCancel(r.Context())
	if err := server.storeLoginAccount(ctx, name, account); errors.Is(err, errLoginRemoved) {
		server.jsonErrCode(w, 409, codeClaudeLogin, "%v", err)
		return
	} else if err != nil {
		if name == store.DefaultClaudeLogin {
			server.cloudAccountCheck.changed()
		}
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if err := server.loginChanged(ctx, name); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.writeClaudeLogin(w, r, name)
}

func (server *Server) handleCheckClaudeLogin(w http.ResponseWriter, r *http.Request) {
	login := server.managedLogin(w, r)
	if login == nil {
		return
	}
	if !login.SignedIn() && (login.Name != store.DefaultClaudeLogin || server.defaultTokenUnset(r.Context())) {
		server.jsonErrCode(w, 409, codeClaudeLogin, "the Claude login %s is signed out: sign it in first", login.Name)
		return
	}
	if server.LoginChecker == nil {
		server.jsonErrCode(w, 409, codeClaudeLogin, "this hub has no login check")
		return
	}
	if err := server.LoginChecker.StartLogin(r.Context(), login.Name); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.writeClaudeLogin(w, r, login.Name)
}

// defaultTokenUnset reports whether the hub has no Claude token, so the
// default login's check has nothing to check.
func (server *Server) defaultTokenUnset(ctx context.Context) bool {
	token, err := loop.HubClaudeToken(ctx, server.Store)
	return err != nil || token == ""
}

// handleRemoveClaudeLogin removes a login, signing it out at the source
// first and ending its sign-in waiting for a code. It is refused while a
// loop runs on it, the default counting every loop that names no login,
// and the refusal names them (ADR-0060).
func (server *Server) handleRemoveClaudeLogin(w http.ResponseWriter, r *http.Request) {
	login := server.managedLogin(w, r)
	if login == nil {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	// under the login's lock, which a loop naming it takes too, so no loop
	// is put on it between the count and the delete
	server.lockLogin(login.Name)
	onIt, err := server.loopsOnLogin(ctx, login.Name)
	if err != nil {
		server.unlockLogin(login.Name)
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if len(onIt) > 0 {
		server.unlockLogin(login.Name)
		server.jsonErrCode(w, 409, codeClaudeLogin, "loops run on the Claude login %s: %s; move them first", login.Name, strings.Join(onIt, ", "))
		return
	}
	server.signOutAtTheSource(ctx, login.Name)
	if server.CloudSignIn != nil {
		server.CloudSignIn.End(login.Name)
	}
	err = server.Store.ClaudeLogins().Delete(ctx, login.Name)
	server.unlockLogin(login.Name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if err := server.loginChanged(ctx, login.Name); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loopsOnLogin names the loops not archived that run on the login called
// name: those that name it, or for the default every loop that names no
// login (ADR-0060).
func (server *Server) loopsOnLogin(ctx context.Context, name string) ([]string, error) {
	loops, err := server.Store.Loops().List(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, loopRecord := range loops {
		if loopRecord.Status != store.StatusArchived && loginKey(loopRecord.ClaudeLogin) == name {
			names = append(names, loopRecord.Name)
		}
	}
	return names, nil
}

func (server *Server) writeClaudeLogin(w http.ResponseWriter, r *http.Request, name string) {
	login, err := server.Store.ClaudeLogins().Get(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		login, err = &store.ClaudeLogin{Name: name}, nil
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	view, err := server.claudeLoginView(r, login)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

// signOutLoginsOf signs out every login the user with userID owned, now
// removed, revoking each at the source and keeping it, as Sign out does: a
// departed person's subscription runs nothing more, and the loops on it
// keep a valid name (Enes, 2026-10-27, #1148). A failure is logged: the
// user is gone either way, and an admin can sign the login out by hand.
func (server *Server) signOutLoginsOf(ctx context.Context, userID string) {
	logins, err := store.SignInsOf(ctx, server.Store.ClaudeLogins(), userID)
	if err != nil {
		server.Log.Warn("Claude logins unread, so a removed user's are still signed in", "err", err)
		return
	}
	for _, login := range logins {
		if err := server.storeLoginAccount(ctx, login.Name, store.CloudAccount{}); err != nil {
			server.Log.Warn("a removed user's Claude login isn't signed out", "login", login.Name, "err", err)
			continue
		}
		if err := server.loginChanged(ctx, login.Name); err != nil {
			server.Log.Warn("a removed user's Claude login signed out, but the hub wasn't told", "login", login.Name, "err", err)
		}
	}
}

// loginKey is the name of the Claude login a loop's claude_login stands
// for: "" is the default.
func loginKey(claudeLogin string) string {
	if claudeLogin == "" {
		return store.DefaultClaudeLogin
	}
	return claudeLogin
}

// loopLoginName is the claude_login a loop stores for the login a request
// names: trimmed, and "" for the default, which a loop names by naming
// none.
func loopLoginName(name string) string {
	if name = strings.TrimSpace(name); name == store.DefaultClaudeLogin {
		return ""
	}
	return name
}

// loopLoginRefusal says why a loop of runtime loopRuntime may not name the
// Claude login login, as loopLoginName stores it, or nil. A bare loop runs
// on the host's own login, and a cloud loop on the default until #1078
// (ADR-0060). Whether the hub holds the login is putOnLogin's to say.
func loopLoginRefusal(login, loopRuntime string) *requestError {
	switch {
	case login == "":
		return nil
	case loopRuntime == store.RuntimeBare:
		return &requestError{409, codeClaudeLogin, "a bare loop runs on this machine's own Claude login, so it names none"}
	case loopRuntime == store.RuntimeCloud:
		return &requestError{409, codeClaudeLogin, "a cloud loop runs on the default Claude login until #1078 lets it name another"}
	}
	return nil
}

// putOnLogin runs write, which puts a loop on the Claude login login, under
// that login's lock, once the hub is seen to hold it: a removal, which
// counts the loops on a login under the same lock, can't land in between.
// A login the hub doesn't hold is a refusal, and write's own error is
// returned as it is, for the caller to answer. The default, login "", is
// written without the lock, which a refresh holds for up to a minute: a
// loop saved on it as the default is removed is held until it is signed in
// again, as every loop is when it is signed out.
func (server *Server) putOnLogin(ctx context.Context, login string, write func() error) (*requestError, error) {
	if login == "" {
		return nil, write()
	}
	server.lockLogin(login)
	defer server.unlockLogin(login)
	_, err := server.Store.ClaudeLogins().Get(ctx, login)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return &requestError{400, codeClaudeLogin, fmt.Sprintf("the hub holds no Claude login named %s", login)}, nil
	case err != nil:
		return &requestError{500, "", err.Error()}, nil
	}
	return nil, write()
}
