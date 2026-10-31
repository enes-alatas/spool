package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// CloudSessions reads a cloud loop's session on claude.ai (ADR-0056).
type CloudSessions interface {
	// SessionURL is the loop's current session page, "" while it has none.
	SessionURL(loopID string) string
	// Turn is the loop's turn that may be running in a session, nil when
	// the hub knows of none (ADR-0059).
	Turn(loopID string) *CloudTurn
}

// CloudTurn is a cloud loop's turn that may be running in its session.
// The CLI can't interrupt a session, so a stop, a kill or a rotation leaves
// a turn running in the VM, out of the hub's hearing: CutOff. Since is the
// hub's send, which a turn about to run shares with one running; Until is
// its quiet bound, when the hub stops counting it. SessionURL is the turn's
// session, where the operator can interrupt it, which after a rotation is
// not the loop's current one. Times are Unix milliseconds.
type CloudTurn struct {
	Since      int64  `json:"since"`
	Until      int64  `json:"until"`
	SessionURL string `json:"session_url"`
	CutOff     bool   `json:"cut_off"`
}

// cloudReady is nil when Settings → Cloud is green, as its page shows it:
// the public address's last check passed, and an account is signed in
// whose last check passed. Otherwise it says which step to finish. Each
// cloud loop names its own environment, which it needs before it wakes,
// not before it is made (ADR-0061).
func (server *Server) cloudReady(ctx context.Context) error {
	address, err := server.storedCloudAddress(ctx)
	if err != nil {
		return err
	}
	if address == "" {
		return errors.New("set the hub's public address in Settings → Cloud first")
	}
	if check := server.cloudCheck.result(address); check == nil || check.State != CloudCheckOK {
		return errors.New("the public address in Settings → Cloud hasn't passed its check")
	}
	set, err := server.cloudAccountSet(ctx)
	if err != nil {
		return err
	}
	if !set {
		return errors.New("sign in to a Claude account in Settings → Cloud first")
	}
	if check := server.cloudAccountCheck.result(); check == nil || check.State != CloudCheckOK {
		return errors.New("the Claude account in Settings → Cloud hasn't passed its check")
	}
	return nil
}

// cloudSetupReq is a cloud loop's repos and environment as a create or a
// PATCH names them (ADR-0061). A field left out keeps what the loop has.
type cloudSetupReq struct {
	CloudRepo          *string   `json:"cloud_repo"`
	CloudBranch        *string   `json:"cloud_branch"`
	CloudRepos         *[]string `json:"cloud_repos"`
	CloudEnvironmentID *string   `json:"cloud_environment_id"`
}

func (req cloudSetupReq) given() bool {
	return req.CloudRepo != nil || req.CloudBranch != nil || req.CloudRepos != nil || req.CloudEnvironmentID != nil
}

// codeCloudLoopRunning: a cloud loop's repos or environment changed while
// it isn't paused. A session runs from them, so they change only while
// the loop is stopped (ADR-0061).
const codeCloudLoopRunning = "cloud_loop_running"

// cloudSetup is the loop's setup after req: what it names over what the
// loop has, trimmed, with the branch DefaultCloudBranch once a repo is
// named without one. Each field is checked by ADR-0061's rule, and an
// environment must be on the list of login, the Claude login the loop
// runs on after the request, "" for the default: checked when the
// environment changes, or when moved is set because the login does. One
// pasted while the list can't be read is taken, as Settings → Cloud took
// one. On a login other than the default the runtime checks it again at
// each start, and the loop doesn't start until the list shows it (item 7,
// #1078).
func (server *Server) cloudSetup(ctx context.Context, login string, moved bool, current store.CloudSetup, req cloudSetupReq) (store.CloudSetup, *requestError) {
	setup := current
	if req.CloudRepo != nil {
		setup.Repo = strings.TrimSpace(*req.CloudRepo)
	}
	if req.CloudBranch != nil {
		setup.Branch = strings.TrimSpace(*req.CloudBranch)
	}
	if req.CloudRepos != nil {
		setup.Repos = nil
		for _, repo := range *req.CloudRepos {
			setup.Repos = append(setup.Repos, strings.TrimSpace(repo))
		}
	}
	if req.CloudEnvironmentID != nil {
		setup.EnvironmentID = strings.TrimSpace(*req.CloudEnvironmentID)
	}
	if setup.Repo != "" && setup.Branch == "" {
		setup.Branch = store.DefaultCloudBranch
	}
	if err := setup.Check(); err != nil {
		return setup, &requestError{status: http.StatusBadRequest, msg: err.Error()}
	}
	changed := req.CloudEnvironmentID != nil && setup.EnvironmentID != current.EnvironmentID
	if setup.EnvironmentID != "" && (changed || moved) {
		if listed, err := server.listCloudEnvironments(ctx, login); err == nil &&
			!slices.ContainsFunc(listed, func(environment CloudEnvironment) bool { return environment.ID == setup.EnvironmentID }) {
			return setup, &requestError{status: http.StatusBadRequest, code: codeCloudEnvironmentUnlisted,
				msg: setup.EnvironmentID + " is not one of the cloud environments of " + loginAccount(login)}
		}
	}
	return setup, nil
}

// handleRotateCloudToken is Rotate token (ADR-0061): it stops the cloud
// loop, mints it a new hub token, which refuses the one it held at once,
// and answers with it, once, for the operator to set as the loop's
// environment's secret. The loop stays stopped until the operator starts
// it, once the secret is set: a session started before that would reach
// the hub on the old one. The hub keeps only the token's hash, so the
// redactor doesn't know it, and the answer is text/plain and uncached:
// nothing else the hub writes carries the token.
func (server *Server) handleRotateCloudToken(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	if loopRecord.Runtime != store.RuntimeCloud {
		server.jsonErr(w, http.StatusBadRequest, "loop %s runs on the %s runtime: only a cloud loop's token is set by hand", loopRecord.Name, loopRecord.Runtime)
		return
	}
	if err := server.pauseLoop(r.Context(), loopRecord); err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "stop the loop: %v", err)
		return
	}
	token, err := server.Store.Loops().RotateHubMCPToken(r.Context(), loopRecord.ID)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "rotate the token: %v", err)
		return
	}
	if actor, ok := server.Manager.Get(loopRecord.ID); ok {
		actor.TokenChanged()
	}
	server.Log.Info("cloud loop's token rotated", "loop", loopRecord.Name)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(token))
}

// loginAccount names the account of the Claude login called login, ""
// for the default, as a refusal says it.
func loginAccount(login string) string {
	if login == "" {
		return "the default Claude login's account"
	}
	return "Claude login " + login + "'s account"
}
