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
}

// ErrNoCloudSignIn is a code handed in with no sign-in waiting for one.
var ErrNoCloudSignIn = errors.New("no sign-in is waiting for a code; start one")

// ErrCloudSignInRefused is a code the CLI didn't take.
var ErrCloudSignInRefused = errors.New("the code was not accepted")

// CloudAccountChecker makes the account check: one read-only call to the
// sessions API that spends no turn (ADR-0056).
type CloudAccountChecker interface {
	// CheckAccount reports where the call stopped, "" when it passed, and
	// how.
	CheckAccount(ctx context.Context) (stage, detail string)
}

// Where a failed account check stopped.
const (
	// CloudAccountStageNone: no account is signed in. No call is made.
	CloudAccountStageNone = "no_account"
	// CloudAccountStageRefused: the API refused the account's token, with
	// a 401 or 403: sign in again.
	CloudAccountStageRefused = "refused"
	// CloudAccountStageUnreachable: the API didn't answer.
	CloudAccountStageUnreachable = "unreachable"
	// CloudAccountStageOther: the API answered with something else.
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

// changeCloudAccount stores the account, "" to forget it, and checks it.
// The redactor learns the new tokens before anything else can quote them,
// and the cloud loops are told: with no account they are held.
func (server *Server) changeCloudAccount(w http.ResponseWriter, r *http.Request, raw string) {
	if server.CloudLogin != nil {
		server.CloudLogin.Lock()
	}
	err := server.Store.Settings().Set(r.Context(), store.SettingCloudAccount, raw)
	if server.CloudLogin != nil {
		server.CloudLogin.Unlock()
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.secretsChanged(r.Context())
	if server.CloudGate != nil {
		server.CloudGate.AccountChanged()
	}
	server.CheckCloudAccount(r.Context())
	server.writeCloudSettings(w, r)
}

// CheckCloudAccount makes the account check and records what it found.
func (server *Server) CheckCloudAccount(ctx context.Context) {
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
		if result.Stage, result.Detail = server.CloudAccounts.CheckAccount(ctx); result.Stage == "" {
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
