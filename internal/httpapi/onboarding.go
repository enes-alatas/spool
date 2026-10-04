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

// harnessPillar reads whether claude can log in. The hub never calls claude
// to find out, so it goes by what it has: a loop whose login was refused
// says no, a turn that finished says yes, and before either the credential
// the default runtime needs being in place counts.
func (server *Server) harnessPillar(ctx context.Context, loops []*store.Loop) (pillarView, error) {
	for _, loopRecord := range loops {
		if actor, ok := server.Manager.Get(loopRecord.ID); ok && actor.DownReason() == loop.DownReasonUnauthenticated {
			return pillarView{Reason: "the Claude login was refused on loop " + loopRecord.Name}, nil
		}
	}
	completed, err := server.Store.Turns().AnyCompleted(ctx)
	switch {
	case err != nil:
		return pillarView{}, err
	case completed:
		return pillarView{Done: true, Reason: "a turn authenticated"}, nil
	case server.defaultRuntime() == store.RuntimeBare:
		return pillarView{Done: true, Reason: "bare loops use the host's claude login"}, nil
	}
	token, err := server.claudeToken(ctx)
	switch {
	case err != nil:
		return pillarView{}, err
	case token == "":
		return pillarView{Reason: "no setup-token saved in Settings"}, nil
	}
	return pillarView{Done: true, Reason: "setup-token saved; the first wake confirms it"}, nil
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
	completed, err := server.Store.Turns().AnyCompleted(ctx)
	if err != nil || !completed {
		return pillarView{Reason: "no loop has woken"}, err
	}
	return pillarView{Done: true}, nil
}
