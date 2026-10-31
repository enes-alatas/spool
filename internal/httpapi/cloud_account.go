package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/roomtext"
	"github.com/enes-alatas/spool/internal/store"
)

// CloudSignIn runs the Claude Code CLI's own login for a Claude login
// (ADR-0056, ADR-0060): the hub never holds an OAuth client of its own.
type CloudSignIn interface {
	// Start begins a sign-in for the login called login, started by the
	// user by ("" for the operator token), ending the one it left
	// unfinished, and returns the link the operator opens. It wraps
	// ErrTooManySignIns past the hub's caps.
	Start(ctx context.Context, login, by string) (string, error)
	// Finish hands the login's waiting sign-in the code the page showed and
	// returns what it signed in as. It is ErrNoCloudSignIn with none
	// waiting, and wraps ErrCloudSignInRefused when the CLI didn't take
	// the code.
	Finish(ctx context.Context, login, code string) (store.CloudAccount, error)
	// Available reports whether the CLI the sign-in runs is on this
	// machine. It looks each time it is asked, so installing the CLI needs
	// a reload of the page and no restart of the hub (ADR-0058).
	Available() bool
	// SignOut runs the CLI's own logout on the login, which revokes its
	// refresh token at the source (ADR-0058). A nil error says the logout
	// ran: the CLI doesn't say whether the revoke was accepted.
	SignOut(ctx context.Context, account store.CloudAccount) error
	// End ends the login's sign-in waiting for its code, if any, so a
	// removed login holds no slot under the caps.
	End(login string)
}

// ErrNoCloudSignIn is a code handed in with no sign-in waiting for one.
var ErrNoCloudSignIn = errors.New("no sign-in is waiting for a code; start one")

// ErrCloudSignInRefused is a code the CLI didn't take.
var ErrCloudSignInRefused = errors.New("the code was not accepted")

// ErrTooManySignIns is a sign-in past the hub's caps on those waiting
// for a code: one per user, and a few across the hub.
var ErrTooManySignIns = errors.New("too many sign-ins are waiting for a code")

// CloudAccountChecker makes the account check: ADR-0044's login check on
// the account's token, one haiku answer to a one-word prompt (ADR-0059).
type CloudAccountChecker interface {
	// CheckAccount reports where the check stopped, "" when it passed, and
	// how.
	CheckAccount(ctx context.Context) (stage, detail string)
}

// Where a failed account check stopped.
const (
	// CloudAccountStageNone: no account is signed in. No call is made.
	CloudAccountStageNone = "no_account"
	// CloudAccountStageRefused: the API refused the account's token, in
	// the CLI's words: sign in again.
	CloudAccountStageRefused = "refused"
	// CloudAccountStageOther: the check couldn't run, or ended without
	// saying either way.
	CloudAccountStageOther = "other"
)

// codeCloudSignIn is the reason a sign-in step is refused.
const codeCloudSignIn = "cloud_sign_in"

// CloudAccountCheckResult is the last account check.
type CloudAccountCheckResult struct {
	State string `json:"state"`
	// Stage and Detail say where a failed check stopped, and how.
	Stage     string `json:"stage,omitempty"`
	Detail    string `json:"detail,omitempty"`
	CheckedAt int64  `json:"checked_at"`
}

// cloudAccountCheckState is the last account check, kept in memory as
// the address check is.
type cloudAccountCheckState struct {
	mu   sync.Mutex
	last *CloudAccountCheckResult
	// changes counts the account's sign-ins and removals, so a check that
	// read the account before one doesn't record over the check made
	// after it (#970).
	changes int
}

type cloudSignInView struct {
	// Link is where the operator signs in; the page then asks for the
	// code it shows.
	Link string `json:"link"`
}

type cloudSignInCodeReq struct {
	Code string `json:"code"`
}

// maxSignInCode bounds a sign-in code: the page's code#state is a few
// hundred bytes.
const maxSignInCode = 1 << 10

// signInCodeShaped reports whether code, its edges trimmed, could be what
// the page showed: one word with no control characters, so nothing past
// it reaches the CLI's stdin as a second answer.
func signInCodeShaped(code string) bool {
	code = strings.TrimSpace(code)
	return len(code) <= maxSignInCode && !strings.ContainsFunc(code, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

// startSignIn begins the CLI's login for the login called name, as the
// caller, and answers with its link: 429 past the hub's caps on sign-ins
// waiting for a code.
func (server *Server) startSignIn(w http.ResponseWriter, r *http.Request, name string) {
	by := ""
	if who := callerOf(r); who != nil && who.user != nil {
		by = who.user.ID
	}
	link, err := server.CloudSignIn.Start(r.Context(), name, by)
	switch {
	case errors.Is(err, ErrTooManySignIns):
		server.jsonErrCode(w, 429, codeCloudSignIn, "%v", err)
	case err != nil:
		server.Log.Warn("Claude sign-in didn't start", "login", name, "err", err)
		server.jsonErrCode(w, 502, codeCloudSignIn, "%v", err)
	default:
		writeJSON(w, 200, cloudSignInView{Link: link})
	}
}

// loginChanged tells the hub that the login called name was signed in,
// signed out or removed: the redactor learns its tokens before anything
// else can quote them. A change of the default login reaches further,
// since it is the hub's Claude token and the cloud loops' account: they
// are held with none, the account is checked again, and the login check
// runs on the hub's Claude token, or is cleared with none. Another login
// is checked when it is signed in.
func (server *Server) loginChanged(ctx context.Context, name string) error {
	if name != store.DefaultClaudeLogin {
		server.secretsChanged(ctx)
		_, signedIn, err := store.SignedInAccount(ctx, server.Store.ClaudeLogins(), name)
		if err != nil || !signedIn || server.LoginChecker == nil {
			return err
		}
		return server.LoginChecker.StartLogin(ctx, name)
	}
	server.cloudAccountCheck.changed()
	server.secretsChanged(ctx)
	if server.CloudGate != nil {
		server.CloudGate.SetupChanged()
	}
	server.CheckCloudAccount(ctx)
	token, err := loop.HubClaudeToken(ctx, server.Store)
	if err != nil {
		return err
	}
	return server.loginTokenChanged(ctx, token)
}

// errLoginRemoved is a sign-in that finished after its login was removed.
var errLoginRemoved = errors.New("this Claude login was removed while it signed in")

// storeLoginAccount stores the sign-in of the login called name, the zero
// account to sign it out. The default login is added if the hub doesn't
// hold it yet. Any other login the hub no longer holds was removed while
// it signed in: the new sign-in is revoked at the source and refused with
// errLoginRemoved, so no login comes back without its owner. Sign out
// asks the source to revoke the login before forgetting its tokens, and
// keeps the login: the loops on it keep a valid name (ADR-0060). A
// sign-in of the default login supersedes the setup-token, which goes
// with it (ADR-0058). It all holds the login's lock, so a refresh never
// rotates the tokens in between. A sign-in as another account than the one
// a cloud loop on the login passed its check under fails that check, and
// can revoke its hub token (failChecksOfAnotherAccount, #1230).
func (server *Server) storeLoginAccount(ctx context.Context, name string, account store.CloudAccount) error {
	var resigned []resignedLoop
	defer func() {
		for _, loopRecord := range resigned {
			server.cloudSetupStored(ctx, loopRecord.id)
			actor, ok := server.Manager.Get(loopRecord.id)
			if !ok {
				continue
			}
			// the live process sends into the old account's session: it
			// closes, and the next start doesn't resume that session
			actor.RefreshEnv()
			if loopRecord.revoked {
				actor.TokenChanged()
			}
		}
	}()
	server.lockLogin(name)
	defer server.unlockLogin(name)
	logins := server.Store.ClaudeLogins()
	if account.Token == "" {
		server.signOutAtTheSource(ctx, name)
	}
	err := logins.SetAccount(ctx, name, account)
	switch {
	case !errors.Is(err, store.ErrNotFound):
	case account.Token == "":
		err = nil
	case name == store.DefaultClaudeLogin:
		err = logins.Create(ctx, &store.ClaudeLogin{Name: name, Account: account, CreatedAt: time.Now().UnixMilli()})
	default:
		server.revokeAtTheSource(ctx, name, account)
		return errLoginRemoved
	}
	if err == nil && account.Token != "" && name == store.DefaultClaudeLogin {
		err = server.Store.Settings().Set(ctx, store.SettingClaudeOAuthToken, "")
	}
	if err == nil && account.Token != "" {
		resigned = server.failChecksOfAnotherAccount(ctx, name, account.AccountUUID)
	}
	return err
}

// failChecksOfAnotherAccount fails the passed check of each cloud loop on
// the login called name that ran under an account other than the one
// whose ID is account, and names those loops. Its next wake would send
// into a session the new account doesn't own (#1230). A check that ran
// when the hub didn't know the account's ID fails too. Where both IDs are
// known and differ, the loop's hub token is revoked as well, passed check
// or not, as a move to another login revokes it: the old account's
// environment holds it as its secret. A check run before the hub knew
// the ID revokes nothing, so an upgrade leaves every token in place. The
// caller holds the login's lock.
func (server *Server) failChecksOfAnotherAccount(ctx context.Context, name, account string) []resignedLoop {
	loops, err := server.Store.Loops().List(ctx)
	if err != nil {
		server.Log.Error("cloud loops unread, so their checks stand after a sign-in", "login", name, "err", err)
		return nil
	}
	var failed []resignedLoop
	for _, loopRecord := range loops {
		check := loopRecord.CloudCheck
		if loopRecord.Runtime != store.RuntimeCloud || loginKey(loopRecord.ClaudeLogin) != name ||
			check == nil || check.Account == account {
			continue
		}
		known := check.Account != "" && account != ""
		if !known && check.State != store.CloudCheckOK {
			continue
		}
		resigned := resignedLoop{id: loopRecord.ID}
		detail := resignedDetail(name)
		if known && loopRecord.HasHubToken {
			if err := server.Store.Loops().RevokeHubMCPToken(ctx, loopRecord.ID); err != nil {
				server.Log.Error("a cloud loop's hub token stands after a sign-in as another account", "loop", loopRecord.Name, "err", err)
			} else {
				resigned.revoked = true
				detail = revokedDetail(name)
			}
		}
		failedCheck := *check
		failedCheck.State, failedCheck.Stage, failedCheck.Detail = store.CloudCheckFailed, cloudStepCheck, detail
		failedCheck.CheckedAt = time.Now().UnixMilli()
		if err := server.Store.Loops().SetCloudCheck(ctx, loopRecord.ID, &failedCheck); err != nil {
			server.Log.Error("a cloud loop's check stands after a sign-in as another account", "loop", loopRecord.Name, "err", err)
		} else {
			server.Log.Warn("cloud loop's setup check failed: its Claude login signed in as another account", "loop", loopRecord.Name, "login", name, "token_revoked", resigned.revoked)
		}
		failed = append(failed, resigned)
	}
	return failed
}

// resignedLoop is a cloud loop whose login signed in as another account,
// and whether its hub token was revoked for it.
type resignedLoop struct {
	id      string
	revoked bool
}

// resignedDetail is what a check failed by a sign-in as another account
// says.
func resignedDetail(login string) string {
	return "the Claude login " + login + " signed in as another account since this check ran; its session isn't that account's, so run the check again"
}

// revokedDetail is what a check failed by a sign-in as another account
// says when the loop's hub token was revoked with it.
func revokedDetail(login string) string {
	return "the Claude login " + login + " signed in as another account since this check ran; the old account's environment held the loop's hub token, so it is revoked: pick an environment of the new account's, Rotate token, and run the check again"
}

// loginAccountID is the ID of the account the login called name is signed
// in as, "" when it isn't, or the hub didn't read the ID.
func (server *Server) loginAccountID(ctx context.Context, name string) string {
	account, _, err := store.SignedInAccount(ctx, server.Store.ClaudeLogins(), name)
	if err != nil {
		server.Log.Warn("Claude login unread", "login", name, "err", err)
	}
	return account.AccountUUID
}

// signOutAtTheSource runs the CLI's logout on the login called name, which
// revokes its refresh token (ADR-0058). A failure is logged, not returned:
// Sign out forgets the tokens whatever the source says, and the page says
// where claude.ai lists the account's sign-ins. A login with no refresh
// token has nothing to revoke. The caller holds the login's lock.
func (server *Server) signOutAtTheSource(ctx context.Context, name string) {
	if server.CloudSignIn == nil {
		return
	}
	account, signedIn, err := store.SignedInAccount(ctx, server.Store.ClaudeLogins(), name)
	if err != nil {
		server.Log.Warn("Claude login unread, so not revoked at the source; revoke it at claude.ai", "login", name, "err", err)
		return
	}
	if signedIn {
		server.revokeAtTheSource(ctx, name, account)
	}
}

// revokeAtTheSource runs the CLI's logout on account, the sign-in of the
// login called name, logging what came of it. An account with no refresh
// token has nothing to revoke.
func (server *Server) revokeAtTheSource(ctx context.Context, name string, account store.CloudAccount) {
	if server.CloudSignIn == nil || account.RefreshToken == "" {
		return
	}
	if err := server.CloudSignIn.SignOut(ctx, account); err != nil {
		server.Log.Warn("Claude login not revoked at the source; revoke it at claude.ai", "login", name, "err", err)
		return
	}
	server.Log.Info("Claude login signed out at the source", "login", name)
}

// cloudAccountCheckTimeout bounds one account check, as ADR-0044's checker
// bounds the same run: the CLI's start and one model answer. Boot and a
// sign-in check on a context that never ends.
const cloudAccountCheckTimeout = 90 * time.Second

// CheckCloudAccount makes the account check and records what it found.
func (server *Server) CheckCloudAccount(ctx context.Context) {
	changes := server.cloudAccountCheck.changesSoFar()
	ctx, cancel := context.WithTimeout(ctx, cloudAccountCheckTimeout)
	defer cancel()
	result := CloudAccountCheckResult{State: CloudCheckFailed, CheckedAt: time.Now().UnixMilli()}
	switch set, err := server.cloudAccountSet(ctx); {
	case err != nil:
		server.Log.Warn("cloud account unread; not checked", "err", err)
		return
	case !set:
		result.Stage = CloudAccountStageNone
	case server.CloudAccounts == nil:
		result.Stage, result.Detail = CloudAccountStageOther, "this hub has no cloud runtime to check the account with"
	default:
		stage, detail := server.CloudAccounts.CheckAccount(ctx)
		// the detail quotes the CLI, not the hub's words (#959)
		if result.Stage, result.Detail = stage, roomtext.Plain(detail); result.Stage == "" {
			result.State = CloudCheckOK
		} else {
			server.Log.Warn("cloud account check failed", "stage", result.Stage, "detail", result.Detail)
		}
	}
	server.cloudAccountCheck.record(&result, changes)
}

// changed records that the account was signed in or removed.
func (state *cloudAccountCheckState) changed() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.changes++
}

// changesSoFar is how many times the account has changed, which a check
// takes before it reads the account.
func (state *cloudAccountCheckState) changesSoFar() int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.changes
}

// record keeps result as the last check, unless the account changed after
// the check took changes: the check made after that change is the one to
// keep, and a check that began before it would overwrite it with the
// account as it was.
func (state *cloudAccountCheckState) record(result *CloudAccountCheckResult, changes int) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if changes == state.changes {
		state.last = result
	}
}

// result is the last account check, nil when none has run.
func (state *cloudAccountCheckState) result() *CloudAccountCheckResult {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.last == nil {
		return nil
	}
	result := *state.last
	return &result
}

// cloudAccountSet reports whether the default Claude login is signed in.
func (server *Server) cloudAccountSet(ctx context.Context) (bool, error) {
	_, signedIn, err := store.SignedInAccount(ctx, server.Store.ClaudeLogins(), store.DefaultClaudeLogin)
	return signedIn, err
}

// LoginLocks holds one Claude login against a refresh of its token
// (loop.LoginRefresh).
type LoginLocks interface {
	Lock(name string)
	Unlock(name string)
}

func (server *Server) lockLogin(name string) {
	if server.LoginLocks != nil {
		server.LoginLocks.Lock(name)
	}
}

func (server *Server) unlockLogin(name string) {
	if server.LoginLocks != nil {
		server.LoginLocks.Unlock(name)
	}
}
