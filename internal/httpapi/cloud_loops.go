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
// the public address's last check passed, an account is signed in whose
// last check passed, and an environment is chosen. Otherwise it says which
// step to finish. An allowlist not confirmed is a warning on the loops, not
// a hold (ADR-0059).
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
	chosen, err := server.storedCloudEnvironment(ctx)
	if err != nil {
		return err
	}
	if chosen.ID == "" {
		return errors.New("choose the environment cloud loops start in, in Settings → Cloud, first")
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
// environment the loop's login lists must be on that list; one pasted
// while the list can't be read is taken, as Settings → Cloud took one,
// and checked again at each start (item 7).
func (server *Server) cloudSetup(ctx context.Context, current store.CloudSetup, req cloudSetupReq) (store.CloudSetup, *requestError) {
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
	if req.CloudEnvironmentID != nil && setup.EnvironmentID != current.EnvironmentID {
		if listed, err := server.listCloudEnvironments(ctx); err == nil &&
			!slices.ContainsFunc(listed, func(environment CloudEnvironment) bool { return environment.ID == setup.EnvironmentID }) {
			return setup, &requestError{status: http.StatusBadRequest, code: codeCloudEnvironmentUnlisted,
				msg: setup.EnvironmentID + " is not one of this account's cloud environments"}
		}
	}
	return setup, nil
}

// handleRotateCloudToken is Rotate token (ADR-0061): it mints the cloud
// loop a new hub token, which refuses the one it held at once, and answers
// with it, once, for the operator to set as the loop's environment's
// secret. The answer is text/plain, which the response redactor passes
// as it is, uncached: no other answer of the hub's carries the token.
// A loop a kill or the want of a token held wakes on it.
func (server *Server) handleRotateCloudToken(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	if loopRecord.Runtime != store.RuntimeCloud {
		server.jsonErr(w, http.StatusBadRequest, "loop %s runs on the %s runtime: only a cloud loop's token is set by hand", loopRecord.Name, loopRecord.Runtime)
		return
	}
	token, err := server.Store.Loops().RotateHubMCPToken(r.Context(), loopRecord.ID)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "rotate the token: %v", err)
		return
	}
	server.secretsChanged(r.Context())
	if actor, ok := server.Manager.Get(loopRecord.ID); ok {
		actor.TokenChanged()
	}
	server.Log.Info("cloud loop's token rotated", "loop", loopRecord.Name)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(token))
}
