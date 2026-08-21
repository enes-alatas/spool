//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEchoTurn: create → message → completed turn with the echoed text, cost
// recorded, session id present.
func TestEchoTurn(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("echoer", nil)
	s.message("echoer", "hello pineapple")

	tn := s.waitTurn("echoer", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "hello pineapple")
	})
	if !strings.HasPrefix(tn.ResultText, "echo: ") {
		t.Errorf("result %q does not start with echo:", tn.ResultText)
	}
	if tn.IsError || tn.SessionID == "" || tn.CostUSD <= 0 {
		t.Errorf("bad turn: %s", dump(tn))
	}
}

// TestIdleReapResumesSameSession: after the idle timeout the process dies;
// the next message must resume the same claude session, not mint a new one.
func TestIdleReapResumesSameSession(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("napper", nil)

	s.message("napper", "first")
	s.waitTurn("napper", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "first")
	})
	s.waitState("napper", "asleep", 30*time.Second)

	s.message("napper", "second")
	s.waitTurn("napper", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "second")
	})

	if ids := sessionIDs(s.completed("napper")); len(ids) != 1 {
		t.Errorf("want 1 session across reap, got %d: %s", len(ids), dump(s.completed("napper")))
	}
}

// TestResumeAcrossServerRestart: kill the whole orchestrator; the session
// must survive into the next server generation (crash-only, QUALITY.md).
func TestResumeAcrossServerRestart(t *testing.T) {
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("phoenix", nil)
	s.message("phoenix", "before restart")
	s.waitTurn("phoenix", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "before restart")
	})
	s.stop()

	s2 := startServer(t, dataDir)
	s2.message("phoenix", "after restart")
	s2.waitTurn("phoenix", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "after restart")
	})

	if ids := sessionIDs(s2.completed("phoenix")); len(ids) != 1 {
		t.Errorf("want same session across restart, got %d: %s", len(ids), dump(s2.completed("phoenix")))
	}
}

// TestSessionLostRecovery: wipe fakeclaude's session state so --resume fails
// (exit 1 + canonical stderr); the runtime must mint a fresh session and the
// message must still be answered.
func TestSessionLostRecovery(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("amnesiac", nil)
	s.message("amnesiac", "remember me")
	s.waitTurn("amnesiac", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "remember me")
	})
	s.waitState("amnesiac", "asleep", 30*time.Second)

	wipeDir(t, s.fkState)

	s.message("amnesiac", "still there?")
	s.waitTurn("amnesiac", 20*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "still there?")
	})

	if ids := sessionIDs(s.completed("amnesiac")); len(ids) != 2 {
		t.Errorf("want 2 sessions (old + recovered), got %d: %s", len(ids), dump(s.completed("amnesiac")))
	}
}

// TestTrailerClampedByMinWake: a [next-wake: 1m] trailer with min_wake=2m
// must schedule ~2m out, and the trailer must be stripped from result_text
// as the loop's outgoing message... (stripping is asserted in the mention
// test via delivery; here we assert the clamp window).
func TestTrailerClampedByMinWake(t *testing.T) {
	s := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "all quiet [next-wake: 1m]\n")
	s.createLoop("pacer", map[string]any{
		"workspace_path": ws,
		"min_wake_sec":   120,
	})
	s.message("pacer", "go")
	s.waitTurn("pacer", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "all quiet")
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		delta := time.Until(time.UnixMilli(s.loop("pacer").NextTickAt))
		if delta > 90*time.Second && delta < 180*time.Second {
			return // clamped to min_wake (120s), not the trailer's 60s
		}
		if time.Now().After(deadline) {
			t.Fatalf("next_tick_at %s out of clamp window", delta)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestMentionRoutedLoopToLoop: loop caller's reply mentions @callee; the hub
// must deliver it as a message-triggered turn on callee.
func TestMentionRoutedLoopToLoop(t *testing.T) {
	s := startServer(t, t.TempDir())
	wsCallee := workspaceWithScript(t, "received.\n")
	s.createLoop("callee", map[string]any{"workspace_path": wsCallee})

	wsCaller := workspaceWithScript(t, "@callee hello from caller\n")
	s.createLoop("caller", map[string]any{"workspace_path": wsCaller})
	s.message("caller", "go talk")

	tn := s.waitTurn("callee", 20*time.Second, func(tn turn) bool {
		return tn.Trigger == "message" && strings.Contains(tn.ResultText, "received.")
	})
	if tn.IsError {
		t.Errorf("callee turn errored: %s", dump(tn))
	}
}

// A loop's context usage comes from the turn that actually ran: the tokens it
// loaded, measured against the window of the model it ran on. An unknown
// model reports its tokens with a zero limit rather than a guessed ratio.
func TestContextUsageOnTheLoopView(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("ctxknown", map[string]any{"model": "claude-haiku-4-5"})
	s.createLoop("ctxunknown", nil) // no model: fakeclaude reports its own name

	if view := s.loop("ctxknown"); view.ContextTokens != 0 || view.ContextLimitTokens != 0 {
		t.Fatalf("a loop with no turns yet should report nothing: %+v", view)
	}

	for _, name := range []string{"ctxknown", "ctxunknown"} {
		s.message(name, "fill some context")
		s.waitTurn(name, 30*time.Second, func(tr turn) bool {
			return strings.Contains(tr.ResultText, "fill some context")
		})
	}

	known := s.loop("ctxknown")
	if known.ContextLimitTokens != 200_000 {
		t.Fatalf("context_limit_tokens = %d, want haiku's 200000", known.ContextLimitTokens)
	}
	if known.ContextTokens <= 0 {
		t.Fatalf("context_tokens = %d, want the last turn's loaded tokens", known.ContextTokens)
	}

	unknown := s.loop("ctxunknown")
	if unknown.ContextLimitTokens != 0 {
		t.Fatalf("an unrecognized model must report an unknown limit, got %d", unknown.ContextLimitTokens)
	}
	if unknown.ContextTokens <= 0 {
		t.Fatalf("context_tokens = %d, want tokens even without a limit", unknown.ContextTokens)
	}
}

// A session that can no longer be resumed — an over-full context window is
// the case we expect — must not become an endless retry against itself. The
// loop rotates onto a fresh session, carries its mission and recent replies
// across, and answers the message that was waiting.
func TestUnresumableSessionRotatesInsteadOfRetrying(t *testing.T) {
	workspace := t.TempDir()
	s := startServer(t, t.TempDir())
	s.createLoop("rotator", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
	})

	s.message("rotator", "first thing")
	first := s.waitTurn("rotator", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "first thing")
	})

	// let the process go away, so the next message has to resume
	s.waitState("rotator", "asleep", 30*time.Second)

	// from here on the session cannot be loaded; a fresh one still can
	if err := os.WriteFile(filepath.Join(workspace, ".fakeclaude-resume-broken"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	s.message("rotator", "second thing")
	second := s.waitTurn("rotator", 90*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "second thing")
	})
	if second.SessionID == first.SessionID {
		t.Fatalf("loop stayed on the session it cannot resume (%s)", first.SessionID)
	}
	if !s.hasEvent("rotator", "session_unusable", 10*time.Second) {
		t.Fatal("rotation was not recorded as a spool event")
	}
}
