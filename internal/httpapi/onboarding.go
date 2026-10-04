package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// onboardingView is the first-run page's read (#580): one fact per pillar,
// each read live, and Completed, which the hub remembers. The page shows
// while Completed is false; a pillar that later reads not done, a revoked
// token say, tells its card so without bringing the page back.
type onboardingView struct {
	Completed bool       `json:"completed"`
	Harness   pillarView `json:"harness"`
	Surface   pillarView `json:"surface"`
	Loops     pillarView `json:"loops"`
}

// pillarView is one pillar: whether it is done, and a short reason saying
// what is missing, or what made it done when there is more than one way.
type pillarView struct {
	Done   bool   `json:"done"`
	Reason string `json:"reason,omitempty"`
	// Checking says a login check is running, so the page can wait on it
	// without keying on the reason's wording. Harness only.
	Checking bool `json:"checking,omitempty"`
}

// handleOnboarding reads the three pillars. Cheap enough to poll: a handful
// of EXISTS queries and the loops' in-memory state. The first read that
// finds all three done stores Completed, so it is a write on a GET, but an
// idempotent one row that only ever happens once per empty fleet.
func (server *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loops, err := server.Store.Loops().List(ctx)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	var view onboardingView
	if view.Harness, err = server.harnessPillar(ctx, loops); err == nil {
		if view.Surface, err = server.surfacePillar(ctx); err == nil {
			view.Loops, err = server.loopsPillar(ctx, loops)
		}
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	_, err = server.Store.Settings().Get(ctx, store.SettingOnboardingCompleted)
	switch {
	case err == nil:
		view.Completed = true
	case !errors.Is(err, store.ErrNotFound):
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if !view.Completed && view.Harness.Done && view.Surface.Done && view.Loops.Done {
		if err := server.Store.Settings().Set(ctx, store.SettingOnboardingCompleted, "1"); err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		view.Completed = true
	}
	writeJSON(w, 200, view)
}

// harnessPillar reads whether claude can log in (ADR-0044). A loop whose
// login is refused now says no, whatever else said yes. Otherwise the newer
// of two pieces of evidence decides: the last login check, and the last turn
// a loop finished, which authenticated too. Saving a setup-token starts a
// check, so a turn from before the save says nothing about the new token.
func (server *Server) harnessPillar(ctx context.Context, loops []*store.Loop) (pillarView, error) {
	for _, loopRecord := range loops {
		if actor, ok := server.Manager.Get(loopRecord.ID); ok && actor.DownReason() == loop.DownReasonUnauthenticated {
			return pillarView{Reason: "the Claude login was refused on loop " + loopRecord.Name}, nil
		}
	}
	bare := server.defaultRuntime() == store.RuntimeBare
	if !bare {
		token, err := server.claudeToken(ctx)
		if err != nil || token == "" {
			return pillarView{Reason: "no setup-token saved in Settings"}, err
		}
	}
	check, err := loop.LastLoginCheck(ctx, server.Store.Settings())
	if err != nil {
		return pillarView{}, err
	}
	lastTurn, err := server.Store.Turns().LastCompleted(ctx)
	if err != nil {
		return pillarView{}, err
	}
	view := harnessEvidence(bare, check, lastTurn)
	view.Checking = check.Status == store.LoginCheckPending
	return view, nil
}

// harnessEvidence reads the newer of the last login check and the last
// completed turn.
func harnessEvidence(bare bool, check store.LoginCheckRecord, lastTurn int64) pillarView {
	if lastTurn > check.At {
		return pillarView{Done: true, Reason: "a turn authenticated"}
	}
	switch check.Status {
	case store.LoginCheckOK:
		return pillarView{Done: true, Reason: "the login check authenticated"}
	case store.LoginCheckRefused:
		return pillarView{Reason: "the login check was refused: " + check.Refusal}
	case store.LoginCheckInconclusive:
		return pillarView{Reason: "the login check did not finish; run it again"}
	case store.LoginCheckPending:
		if bare {
			return pillarView{Reason: "the host's claude login is being checked"}
		}
		return pillarView{Reason: "the setup-token is being checked"}
	}
	if bare {
		return pillarView{Reason: "the host's claude login is not checked yet"}
	}
	return pillarView{Reason: "setup-token saved; not checked yet"}
}

// handleLoginCheck runs the login check on the operator's request and
// answers at once with the read it started from; the outcome reaches the
// harness pillar when the check ends. Each request spends a haiku answer,
// which is why only the operator asks, never a timer (ADR-0044).
func (server *Server) handleLoginCheck(w http.ResponseWriter, r *http.Request) {
	if server.LoginChecker == nil {
		server.jsonErr(w, 503, "this hub runs no login check")
		return
	}
	if server.defaultRuntime() != store.RuntimeBare {
		if token, err := server.claudeToken(r.Context()); err != nil || token == "" {
			server.jsonErrCode(w, 409, codeNoSetupToken, "no setup-token saved in Settings to check")
			return
		}
	}
	if err := server.LoginChecker.Start(r.Context()); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.handleOnboarding(w, r)
}

// surfacePillar reads whether one chat surface has carried a message each
// way. When none has, the reason names the half the closest one is missing.
func (server *Server) surfacePillar(ctx context.Context) (pillarView, error) {
	traffic, err := server.Store.Messages().Traffic(ctx)
	if err != nil {
		return pillarView{}, err
	}
	reason := "no chat surface has carried a message yet"
	for _, surface := range traffic {
		switch {
		case surface.Sent && surface.Received:
			return pillarView{Done: true}, nil
		case surface.Sent:
			reason = "no message received from the operator on " + surface.Surface + " yet"
		case surface.Received:
			reason = "no loop's message has reached " + surface.Surface + " yet"
		}
	}
	return pillarView{Reason: reason}, nil
}

// loopsPillar reads whether a loop has woken and finished a turn.
func (server *Server) loopsPillar(ctx context.Context, loops []*store.Loop) (pillarView, error) {
	if len(loops) == 0 {
		return pillarView{Reason: "no loops"}, nil
	}
	lastTurn, err := server.Store.Turns().LastCompleted(ctx)
	if err != nil || lastTurn == 0 {
		return pillarView{Reason: "no loop has woken"}, err
	}
	return pillarView{Done: true}, nil
}

// loginTokenChanged checks a setup-token as it is saved, so a wrong one
// shows before any loop wakes on it, and forgets the last check when the
// token is removed (ADR-0044). A bare hub's loops do not run on the token,
// so saving one there checks nothing.
func (server *Server) loginTokenChanged(ctx context.Context, token string) error {
	if server.LoginChecker == nil || server.defaultRuntime() == store.RuntimeBare {
		return nil
	}
	if token == "" {
		return server.LoginChecker.Clear(ctx)
	}
	return server.LoginChecker.Start(ctx)
}
