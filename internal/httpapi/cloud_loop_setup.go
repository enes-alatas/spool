package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/roomtext"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// A cloud loop's setup view (ADR-0061, #1182) is what the operator pastes
// into the loop's environment on claude.ai and what the hub checks of it:
// the setup script, the host the loop's hub token is the network secret
// for, the allowed hosts and their confirm, and a check that a session
// started there reaches the hub. The token itself is minted and rotated by
// handleRotateCloudToken, which shows it once; nothing here carries it.

// The setup steps a cloud loop may still need, in the order the view lists
// them.
const (
	// cloudStepSettings: Settings → Cloud isn't green, so no cloud loop can
	// be set up yet.
	cloudStepSettings = "settings"
	// cloudStepRepo and cloudStepEnvironment: the loop names no repo, or no
	// environment.
	cloudStepRepo        = "repo"
	cloudStepEnvironment = "environment"
	// cloudStepToken: the loop holds no hub token: never minted, or killed.
	cloudStepToken = "token"
	// cloudStepAllowlist: the allowed hosts to paste aren't the ones the
	// operator confirmed. The loop isn't held for it: it carries the
	// warning that the hub's allowlist doesn't bind it.
	cloudStepAllowlist = "allowlist"
	// cloudStepCheck: no check passed on the loop's setup as it is now.
	cloudStepCheck = "check"
)

// The reasons a cloud loop's setup routes refuse.
const (
	// codeCloudCheckRefused: a check the loop can't run now, whose message
	// names the step to finish first.
	codeCloudCheckRefused = "cloud_check_refused"
	// codeNotCloudLoop: the loop doesn't run on the cloud runtime.
	codeNotCloudLoop = "not_cloud_loop"
)

// cloudLoopSetupView is a cloud loop's setup view.
type cloudLoopSetupView struct {
	// Steps are what the loop still needs, in order; empty once its setup
	// is done.
	Steps []string `json:"steps"`
	// Settings says what Settings → Cloud still needs, "" when it is green.
	Settings string `json:"settings,omitempty"`
	// Script is the setup script to paste into the environment, the same
	// text for every cloud loop of the hub, holding no secret. "" while the
	// hub has no public address.
	Script string `json:"script"`
	// SecretHost is the host the loop's hub token is set for as the
	// environment's network secret: the public address's host.
	SecretHost string `json:"secret_host"`
	// Token says whether the loop holds a hub token, never what it is.
	Token cloudLoopTokenView `json:"token"`
	// Allowlist is the allowed hosts to paste, and whether the operator
	// confirmed pasting them into this loop's environment.
	Allowlist cloudLoopAllowlistView `json:"allowlist"`
	// Check is the loop's last setup check, absent before the first.
	Check *cloudLoopCheckView `json:"check,omitempty"`
}

type cloudLoopTokenView struct {
	Set bool `json:"set"`
}

// cloudLoopAllowlistView is built from the hub's allowlist on every request
// and never cached, as Settings → Cloud built it (ADR-0059).
type cloudLoopAllowlistView struct {
	Text        string   `json:"text"`
	NotApplied  []string `json:"not_applied"`
	Digest      string   `json:"digest"`
	Confirmed   bool     `json:"confirmed"`
	ConfirmedAt int64    `json:"confirmed_at,omitempty"`
}

// cloudLoopCheckView is a check as the view shows it. State is "running"
// while one runs, then ok or failed. Current says whether a finished check
// was of the loop's setup as it is now, against the public address as it
// is now: a stale pass holds the loop as no check does.
type cloudLoopCheckView struct {
	State     string `json:"state"`
	Stage     string `json:"stage,omitempty"`
	Detail    string `json:"detail,omitempty"`
	CheckedAt int64  `json:"checked_at,omitempty"`
	Current   bool   `json:"current"`
}

// cloudCheckRunning is the stage-less state of a check under way.
const cloudCheckRunning = "running"

// cloudLoopChecks are the setup checks under way, by loop: one at a time
// each.
type cloudLoopChecks struct {
	mu      sync.Mutex
	running map[string]bool
}

func (checks *cloudLoopChecks) begin(loopID string) bool {
	checks.mu.Lock()
	defer checks.mu.Unlock()
	if checks.running[loopID] {
		return false
	}
	if checks.running == nil {
		checks.running = map[string]bool{}
	}
	checks.running[loopID] = true
	return true
}

func (checks *cloudLoopChecks) end(loopID string) {
	checks.mu.Lock()
	defer checks.mu.Unlock()
	delete(checks.running, loopID)
}

func (checks *cloudLoopChecks) underWay(loopID string) bool {
	checks.mu.Lock()
	defer checks.mu.Unlock()
	return checks.running[loopID]
}

// cloudLoop reads the named loop for a setup route, refusing one that
// isn't a cloud loop.
func (server *Server) cloudLoop(w http.ResponseWriter, r *http.Request) *store.Loop {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return nil
	}
	if loopRecord.Runtime != store.RuntimeCloud {
		server.jsonErrCode(w, http.StatusBadRequest, codeNotCloudLoop, "loop %s runs on the %s runtime: only a cloud loop has a setup view", loopRecord.Name, loopRecord.Runtime)
		return nil
	}
	return loopRecord
}

func (server *Server) handleGetCloudLoopSetup(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.cloudLoop(w, r)
	if loopRecord == nil {
		return
	}
	server.answerCloudLoopSetup(w, r, http.StatusOK, loopRecord.ID)
}

// cloudLoopSetup is the loop's setup view as the store and the hub's
// settings hold it now, with its check shown under way when running.
func (server *Server) cloudLoopSetup(ctx context.Context, loopRecord *store.Loop, running bool) (cloudLoopSetupView, error) {
	view := cloudLoopSetupView{Steps: []string{}, Token: cloudLoopTokenView{Set: loopRecord.HasHubToken}}
	address, err := server.storedCloudAddress(ctx)
	if err != nil {
		return view, err
	}
	if address != "" && server.CloudHooks != nil {
		if view.Script, err = server.CloudHooks.SetupScript(address + "/mcp"); err != nil {
			return view, err
		}
		if parsed, err := url.Parse(address); err == nil {
			view.SecretHost = parsed.Host
		}
	}
	if err := server.cloudReady(ctx); err != nil {
		view.Settings = err.Error()
		view.Steps = append(view.Steps, cloudStepSettings)
	}
	if loopRecord.CloudRepo == "" {
		view.Steps = append(view.Steps, cloudStepRepo)
	}
	if loopRecord.CloudEnvironmentID == "" {
		view.Steps = append(view.Steps, cloudStepEnvironment)
	}
	if !loopRecord.HasHubToken {
		view.Steps = append(view.Steps, cloudStepToken)
	}
	allowlist, err := server.cloudAllowlist(ctx)
	if err != nil {
		return view, err
	}
	view.Allowlist = cloudLoopAllowlistView{
		Text: allowlist.text(), NotApplied: allowlist.notApplied, Digest: allowlist.digest(),
	}
	if view.Allowlist.NotApplied == nil {
		view.Allowlist.NotApplied = []string{}
	}
	if loopRecord.CloudAllowlistDigest == view.Allowlist.Digest {
		view.Allowlist.Confirmed, view.Allowlist.ConfirmedAt = true, loopRecord.CloudAllowlistConfirmedAt
	} else {
		view.Allowlist.Confirmed = false
		view.Steps = append(view.Steps, cloudStepAllowlist)
	}
	current := cloudCheckCurrent(loopRecord, address)
	switch check := loopRecord.CloudCheck; {
	case running:
		view.Check = &cloudLoopCheckView{State: cloudCheckRunning}
	case check != nil:
		view.Check = &cloudLoopCheckView{State: check.State, Stage: check.Stage, Detail: check.Detail, CheckedAt: check.CheckedAt, Current: current}
	}
	if !current || loopRecord.CloudCheck.State != store.CloudCheckOK {
		view.Steps = append(view.Steps, cloudStepCheck)
	}
	return view, nil
}

// cloudCheckCurrent reports whether the loop's last check was of its setup
// as it is now, against address.
func cloudCheckCurrent(loopRecord *store.Loop, address string) bool {
	check := loopRecord.CloudCheck
	return check != nil && check.For == store.CloudCheckFor(loopRecord) && check.Address == address
}

// cloudLoopAllowlistUnconfirmed reports whether the cloud loop carries the
// warning that the hub's egress allowlist doesn't bind it: the operator
// hasn't confirmed pasting the allowed hosts as they are now into its
// environment (ADR-0061).
func (server *Server) cloudLoopAllowlistUnconfirmed(ctx context.Context, loopRecord *store.Loop) bool {
	allowlist, err := server.cloudAllowlist(ctx)
	return err != nil || loopRecord.CloudAllowlistDigest != allowlist.digest()
}

// handleConfirmCloudLoopAllowlist records that the operator pasted the
// allowed hosts whose digest the request names into the loop's
// environment. A digest that isn't the current text's is refused: the
// text moved since the page showed it (ADR-0061).
func (server *Server) handleConfirmCloudLoopAllowlist(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.cloudLoop(w, r)
	if loopRecord == nil {
		return
	}
	var req cloudAllowlistConfirmReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, http.StatusBadRequest, "bad json: %v", err)
		return
	}
	allowlist, err := server.cloudAllowlist(r.Context())
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if req.Digest != allowlist.digest() {
		server.jsonErrCode(w, http.StatusConflict, codeCloudAllowlistStale,
			"the list to paste has changed since the page showed it; paste the list shown now and confirm again")
		return
	}
	if err := server.Store.Loops().ConfirmCloudAllowlist(r.Context(), loopRecord.ID, req.Digest, time.Now().UnixMilli()); err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	server.answerCloudLoopSetup(w, r, http.StatusOK, loopRecord.ID)
}

// handleCheckCloudLoop starts the loop's setup check and answers 202 with
// the view, its check running. The check starts one session in the loop's
// environment (cloud.Runtime's CheckSetup), which can take the start's
// whole bound, so it runs on past the request; the view reads its result.
// It runs only once the loop's setup is complete and the loop is running:
// a paused loop's token is refused, so its check couldn't pass.
func (server *Server) handleCheckCloudLoop(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.cloudLoop(w, r)
	if loopRecord == nil {
		return
	}
	refuse := func(msg string, args ...any) {
		server.jsonErrCode(w, http.StatusConflict, codeCloudCheckRefused, msg, args...)
	}
	if err := server.cloudReady(r.Context()); err != nil {
		refuse("%v", err)
		return
	}
	switch {
	case server.CloudHooks == nil:
		refuse("this hub runs no cloud loops")
		return
	case loopRecord.CloudRepo == "" || loopRecord.CloudBranch == "" || loopRecord.CloudEnvironmentID == "":
		refuse("name the loop's repo and environment first")
		return
	case !loopRecord.HasHubToken:
		refuse("mint the loop's hub token and set it as the environment's network secret first")
		return
	case loopRecord.Status != store.StatusActive:
		refuse("start the loop first: the hub refuses a stopped loop's token, so its check couldn't pass")
		return
	}
	address, err := server.storedCloudAddress(r.Context())
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if !server.cloudLoopChecks.begin(loopRecord.ID) {
		server.answerCloudLoopSetup(w, r, http.StatusAccepted, loopRecord.ID)
		return
	}
	checked := *loopRecord
	go server.runCloudLoopCheck(context.WithoutCancel(r.Context()), &checked, address)
	server.answerCloudLoopSetup(w, r, http.StatusAccepted, loopRecord.ID)
}

// runCloudLoopCheck runs the loop's check and records it for the setup it
// ran on, then tells the loop's actor, which a pass lets go when the rest
// of its setup is done.
func (server *Server) runCloudLoopCheck(ctx context.Context, loopRecord *store.Loop, address string) {
	defer server.cloudLoopChecks.end(loopRecord.ID)
	login := loginKey(loopRecord.ClaudeLogin)
	account := server.loginAccountID(ctx, login)
	source := runtime.CloudStart{Repo: loopRecord.CloudRepo, Branch: loopRecord.CloudBranch, EnvironmentID: loopRecord.CloudEnvironmentID, Login: loopRecord.ClaudeLogin}
	stage, detail := server.CloudHooks.CheckSetup(ctx, loopRecord.ID, source, loop.CloudCheckMessage)
	check := &store.CloudCheck{State: store.CloudCheckOK, CheckedAt: time.Now().UnixMilli(), Address: address, For: store.CloudCheckFor(loopRecord), Account: account}
	// under the login's lock, which a sign-in takes too, so one landing
	// as another account fails this check rather than missing it
	server.lockLogin(login)
	if stage == "" && server.loginAccountID(ctx, login) != account {
		stage, detail = cloudStepCheck, resignedDetail(login)
	}
	if stage != "" {
		// the CLI's line quotes what claude.ai answered (#959)
		check.State, check.Stage, check.Detail = store.CloudCheckFailed, stage, roomtext.Plain(detail)
		server.Log.Warn("cloud loop's setup check failed", "loop", loopRecord.Name, "stage", stage, "detail", detail)
	} else {
		server.Log.Info("cloud loop's setup check passed", "loop", loopRecord.Name)
	}
	err := server.Store.Loops().SetCloudCheck(ctx, loopRecord.ID, check)
	server.unlockLogin(login)
	if err != nil {
		server.Log.Error("record a cloud loop's setup check", "loop", loopRecord.Name, "err", err)
		return
	}
	server.cloudSetupStored(ctx, loopRecord.ID)
}

// CloudSessionMisplaced takes the cloud runtime's word that a start of the
// loop's ran in an environment other than its own (cloud.Runtime's
// Misplaced): it records a failed setup check at the environment, saying
// detail, so the loop holds until a check passes rather than making a
// stray session at every wake (#1232).
func (server *Server) CloudSessionMisplaced(loopID, detail string) {
	ctx := context.Background()
	loopRecord, err := server.Store.Loops().Get(ctx, loopID)
	if err != nil {
		server.Log.Error("read a misplaced cloud loop", "loop", loopID, "err", err)
		return
	}
	address, _ := server.storedCloudAddress(ctx)
	check := &store.CloudCheck{
		State: store.CloudCheckFailed, Stage: cloudStepEnvironment, Detail: roomtext.Plain(detail),
		CheckedAt: time.Now().UnixMilli(), Address: address, For: store.CloudCheckFor(loopRecord),
	}
	server.Log.Warn("cloud loop's start ran in another environment", "loop", loopRecord.Name, "detail", detail)
	if err := server.Store.Loops().SetCloudCheck(ctx, loopID, check); err != nil {
		server.Log.Error("record a misplaced cloud loop's check", "loop", loopRecord.Name, "err", err)
		return
	}
	server.cloudSetupStored(ctx, loopID)
}

// cloudSetupStored tells the loop's actor that its stored setup changed.
func (server *Server) cloudSetupStored(ctx context.Context, loopID string) {
	stored, err := server.Store.Loops().Get(ctx, loopID)
	if err != nil {
		return
	}
	if actor, ok := server.Manager.Get(loopID); ok {
		actor.UpdateLoop(stored)
	}
}

// answerCloudLoopSetup answers with the loop's setup view, read again.
// Whether a check is under way is asked before the row is read: a check
// stores its result before it ends, so one that ends between the two has
// its result in the row. Asked after, a check ending between the two left
// the row from before it, shown as the latest (#1243).
func (server *Server) answerCloudLoopSetup(w http.ResponseWriter, r *http.Request, status int, loopID string) {
	running := server.cloudLoopChecks.underWay(loopID)
	stored, err := server.Store.Loops().Get(r.Context(), loopID)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	view, err := server.cloudLoopSetup(r.Context(), stored, running)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, view)
}
