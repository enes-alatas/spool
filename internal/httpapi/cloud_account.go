package httpapi

import (
	"context"
	"encoding/json"
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

// CloudSignIn runs the Claude Code CLI's own login for the account cloud
// loops spend (ADR-0056): the hub never holds an OAuth client of its own.
type CloudSignIn interface {
	// Start begins a sign-in, ending any left unfinished, and returns the
	// link the operator opens.
	Start(ctx context.Context) (string, error)
	// Finish hands the waiting sign-in the code the page showed and
	// returns what it signed in as. It is ErrNoCloudSignIn with none
	// waiting, and wraps ErrCloudSignInRefused when the CLI didn't take
	// the code.
	Finish(ctx context.Context, code string) (store.CloudAccount, error)
	// Available reports whether the CLI the sign-in runs is on this
	// machine. It looks each time it is asked, so installing the CLI needs
	// a reload of the page and no restart of the hub (ADR-0058).
	Available() bool
	// SignOut runs the CLI's own logout on the login, which revokes its
	// refresh token at the source (ADR-0058). A nil error says the logout
	// ran: the CLI doesn't say whether the revoke was accepted.
	SignOut(ctx context.Context, account store.CloudAccount) error
}

// ErrNoCloudSignIn is a code handed in with no sign-in waiting for one.
var ErrNoCloudSignIn = errors.New("no sign-in is waiting for a code; start one")

// ErrCloudSignInRefused is a code the CLI didn't take.
var ErrCloudSignInRefused = errors.New("the code was not accepted")

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

// handleStartCloudSignIn begins the CLI's login and answers with its link.
func (server *Server) handleStartCloudSignIn(w http.ResponseWriter, r *http.Request) {
	if server.CloudSignIn == nil {
		server.jsonErrCode(w, 409, codeCloudSignIn, "this hub can't sign in to a Claude account")
		return
	}
	link, err := server.CloudSignIn.Start(r.Context())
	if err != nil {
		server.Log.Warn("cloud sign-in didn't start", "err", err)
		server.jsonErrCode(w, 502, codeCloudSignIn, "%v", err)
		return
	}
	writeJSON(w, 200, cloudSignInView{Link: link})
}

// handleFinishCloudSignIn hands the CLI the code, stores what it signed
// in as, sealed, and checks it.
func (server *Server) handleFinishCloudSignIn(w http.ResponseWriter, r *http.Request) {
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
	account, err := server.CloudSignIn.Finish(r.Context(), req.Code)
	switch {
	case errors.Is(err, ErrNoCloudSignIn):
		server.jsonErrCode(w, 409, codeCloudSignIn, "%v", err)
		return
	case errors.Is(err, ErrCloudSignInRefused):
		server.jsonErrCode(w, 400, codeCloudSignIn, "%v", err)
		return
	case err != nil:
		server.Log.Warn("cloud sign-in didn't finish", "err", err)
		server.jsonErr(w, 500, "%v", err)
		return
	}
	raw, err := json.Marshal(account)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.changeCloudAccount(w, r, string(raw))
}

func (server *Server) handleRemoveCloudAccount(w http.ResponseWriter, r *http.Request) {
	server.changeCloudAccount(w, r, "")
}

func (server *Server) handleCheckCloudAccount(w http.ResponseWriter, r *http.Request) {
	server.CheckCloudAccount(r.Context())
	server.writeCloudSettings(w, r)
}

// changeCloudAccount stores the Claude login, "" to sign it out, and
// checks it. Sign out asks the source to revoke the login before
// forgetting it, and a sign-in supersedes the setup-token, which the hub
// clears with it (ADR-0058). Both hold the login's lock, so a refresh never
// rotates the tokens in between. The redactor learns the new tokens before
// anything else can quote them, and the cloud loops are told: with no
// account they are held. The login check then runs on the hub's Claude
// token, or is cleared with none, as saving a setup-token did. A closed
// tab doesn't cut the change short: a logout or a store write left halfway
// would leave the login neither kept nor forgotten.
func (server *Server) changeCloudAccount(w http.ResponseWriter, r *http.Request, raw string) {
	ctx := context.WithoutCancel(r.Context())
	if server.CloudLogin != nil {
		server.CloudLogin.Lock()
	}
	if raw == "" {
		server.signOutAtTheSource(ctx)
	}
	err := server.Store.Settings().Set(ctx, store.SettingCloudAccount, raw)
	if err == nil && raw != "" {
		err = server.Store.Settings().Set(ctx, store.SettingClaudeOAuthToken, "")
	}
	if server.CloudLogin != nil {
		server.CloudLogin.Unlock()
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.secretsChanged(ctx)
	if server.CloudGate != nil {
		server.CloudGate.AccountChanged()
	}
	server.CheckCloudAccount(ctx)
	token, err := loop.HubClaudeToken(ctx, server.Store.Settings())
	if err == nil {
		err = server.loginTokenChanged(ctx, token)
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.writeCloudSettings(w, r)
}

// signOutAtTheSource runs the CLI's logout on the stored login, which
// revokes its refresh token (ADR-0058). A failure is logged, not returned:
// Sign out forgets the login whatever the source says, and the page says
// where claude.ai lists the account's sign-ins. A login with no refresh
// token has nothing to revoke. The caller holds the login's lock.
func (server *Server) signOutAtTheSource(ctx context.Context) {
	if server.CloudSignIn == nil {
		return
	}
	raw, err := server.Store.Settings().Get(ctx, store.SettingCloudAccount)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return
	}
	var account store.CloudAccount
	if err == nil {
		err = json.Unmarshal([]byte(raw), &account)
	}
	if err != nil {
		server.Log.Warn("Claude login unread, so not revoked at the source; revoke it at claude.ai", "err", err)
		return
	}
	if account.RefreshToken == "" {
		return
	}
	if err := server.CloudSignIn.SignOut(ctx, account); err != nil {
		server.Log.Warn("Claude login not revoked at the source; revoke it at claude.ai", "err", err)
		return
	}
	server.Log.Info("Claude login signed out at the source")
}

// cloudAccountCheckTimeout bounds one account check, as ADR-0044's checker
// bounds the same run: the CLI's start and one model answer. Boot and a
// sign-in check on a context that never ends.
const cloudAccountCheckTimeout = 90 * time.Second

// CheckCloudAccount makes the account check and records what it found.
func (server *Server) CheckCloudAccount(ctx context.Context) {
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
	server.cloudAccountCheck.mu.Lock()
	server.cloudAccountCheck.last = &result
	server.cloudAccountCheck.mu.Unlock()
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

// cloudAccountSet reports whether an account is signed in. Remove stores
// "", which reads as none.
func (server *Server) cloudAccountSet(ctx context.Context) (bool, error) {
	raw, err := server.Store.Settings().Get(ctx, store.SettingCloudAccount)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return raw != "", err
}
