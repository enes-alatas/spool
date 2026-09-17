//go:build integration

package itest

import (
	"encoding/json"
	"fmt"
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

// TestLoopScriptedBySecret: the turn script can arrive as a loop secret
// rather than a file in the workspace. That is the route a contained loop has
// — its working directory is inside its workstation, out of a test's reach —
// and this row keeps it honest on the runtime CI always exercises, so a
// broken env route cannot hide behind a skipped docker suite (#117).
func TestLoopScriptedBySecret(t *testing.T) {
	s := startServer(t, t.TempDir())
	// a workspace with no .fakeclaude in it: the script has one way in
	s.createLoop("enveloped", map[string]any{
		"workspace_path": t.TempDir(),
		"workspace_mode": "dir",
	})
	s.scriptLoop("enveloped", "!ctx 20000 scripted through the environment\n")

	s.message("enveloped", "say something of your own")
	answered := s.waitTurn("enveloped", 30*time.Second, func(tr turn) bool {
		return tr.Trigger == "message"
	})
	if !strings.Contains(answered.ResultText, "scripted through the environment") {
		t.Fatalf("the secret's script did not reach the turn:\n%s", answered.ResultText)
	}
	if answered.ContextTokens != 20000 {
		t.Fatalf("scripted context = %d tokens, want the directive's 20000", answered.ContextTokens)
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

// TestSendRoutedLoopToLoop: caller's turn group-sends a message mentioning
// @callee through the hub's MCP endpoint; the hub must deliver it as a
// message-triggered turn on callee. Caller's own final reply is a status
// note and must reach nobody — even when it names a peer.
func TestSendRoutedLoopToLoop(t *testing.T) {
	s := startServer(t, t.TempDir())
	wsCallee := workspaceWithScript(t, "received.\n")
	s.createLoop("callee", map[string]any{"workspace_path": wsCallee})

	wsCaller := workspaceWithScript(t,
		`!send {"destination":"group","text":"@callee hello from caller"} @callee note to self about callee`+"\n")
	s.createLoop("caller", map[string]any{"workspace_path": wsCaller})
	s.message("caller", "go talk")

	tn := s.waitTurn("callee", 20*time.Second, func(tn turn) bool {
		return tn.Trigger == "message" && strings.Contains(tn.ResultText, "received.")
	})
	if tn.IsError {
		t.Errorf("callee turn errored: %s", dump(tn))
	}
	// The status note ("note to self…") was never a message: not stored,
	// not delivered, its @mention routed nowhere.
	for _, m := range s.activity() {
		if strings.Contains(m.Text, "note to self") {
			t.Errorf("status note was stored as a message: %s", dump(m))
		}
	}
	callee := s.completed("callee")
	if len(callee) > 1 {
		for _, tn := range callee {
			if strings.Contains(tn.ResultText, "note to self") {
				t.Errorf("status note reached callee: %s", dump(tn))
			}
		}
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

// TestEventsNewestWindow: a loop's timeline is read from the recent end.
// after_id follows the tail from the oldest event, which is what a page
// polling for new ones wants and what the page opening on a long-lived loop
// got by accident — the first events of its life, never the current ones
// (#119). before_id asks for the newest window instead, and pages older from
// there; both forms come back oldest first, so a reader assembles a page the
// same way whichever end it asked from.
func TestEventsNewestWindow(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("historian", nil)

	// a few turns is already tens of events: envelopes, results, spool events
	for _, text := range []string{"one", "two", "three"} {
		s.message("historian", text)
		s.waitTurn("historian", 30*time.Second, func(tr turn) bool {
			return strings.Contains(tr.ResultText, text)
		})
	}

	all := s.eventsQuery("historian", "after_id=0&limit=500")
	if len(all) < 12 {
		t.Fatalf("expected a timeline worth windowing, got %d events", len(all))
	}

	newest := s.eventsQuery("historian", "before_id=0&limit=5")
	if len(newest) != 5 {
		t.Fatalf("newest window returned %d events, want 5", len(newest))
	}
	if newest[len(newest)-1].ID != all[len(all)-1].ID {
		t.Fatalf("newest window ends at %d, want the timeline's last event %d",
			newest[len(newest)-1].ID, all[len(all)-1].ID)
	}
	for i := 1; i < len(newest); i++ {
		if newest[i].ID <= newest[i-1].ID {
			t.Fatalf("newest window is not oldest first: %d then %d", newest[i-1].ID, newest[i].ID)
		}
	}

	// the same limit from the other end reads the loop's first events: the
	// two forms are different questions, and after_id still answers its own
	oldest := s.eventsQuery("historian", "after_id=0&limit=5")
	if oldest[0].ID != all[0].ID || oldest[0].ID == newest[0].ID {
		t.Fatalf("after_id no longer reads from the oldest end: %d vs %d", oldest[0].ID, all[0].ID)
	}

	// and the window pages older from its own first id
	older := s.eventsQuery("historian", fmt.Sprintf("before_id=%d&limit=5", newest[0].ID))
	if len(older) == 0 {
		t.Fatal("paging before the newest window returned nothing")
	}
	if older[len(older)-1].ID >= newest[0].ID {
		t.Fatalf("page before %d ends at %d; it must be strictly older",
			newest[0].ID, older[len(older)-1].ID)
	}
}

// TestContextRotationAtQuietBoundary: turns that fill the window past the arm
// threshold make the loop write a handoff note and rotate at a quiet
// boundary; the next message lands on a fresh session whose first turn
// carries the note, and the note itself never enters the message stream
// (ADR-0022). Every fakeclaude turn echoes and reports 100k of a 200k
// window (50%, past the 40% arm default, below the 70% force ceiling).
func TestContextRotationAtQuietBoundary(t *testing.T) {
	workspace := workspaceWithScript(t, "!ctx 100000\n")
	s := startServer(t, t.TempDir())
	s.createLoop("shedder", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
		"model":          "haiku",
	})

	s.message("shedder", "fill it")
	s.waitTurn("shedder", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "fill it")
	})

	// the queue is empty, so the handoff turn and the rotation follow on
	// their own; the echoed reply proves the rotation request reached the loop
	handoff := s.waitTurn("shedder", 30*time.Second, func(tr turn) bool {
		return tr.Trigger == "rotation" && strings.Contains(tr.ResultText, "handoff note")
	})
	if !s.hasEvent("shedder", "context_rotated", 30*time.Second) {
		t.Fatal("rotation was not recorded as a spool event")
	}

	// the next message runs on a fresh session, seeded with the note the old
	// session wrote — the echo shows the preamble carrying it
	s.message("shedder", "carry on")
	carried := s.waitTurn("shedder", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "carry on")
	})
	if carried.SessionID == handoff.SessionID {
		t.Fatalf("work after the rotation stayed on the retired session %s", handoff.SessionID)
	}
	if !strings.Contains(carried.ResultText, "your context was rotated") ||
		!strings.Contains(carried.ResultText, "Handoff note from your previous session") {
		t.Fatalf("fresh session did not carry the handoff note:\n%s", carried.ResultText)
	}

	// the handoff reply is a note to the successor, not an outgoing message
	var msgs []struct {
		Origin string `json:"origin"`
		Text   string `json:"text"`
	}
	s.mustJSON("GET", "/api/activity?limit=100", nil, &msgs)
	for _, m := range msgs {
		if m.Origin == "loop" && strings.HasPrefix(m.Text, "echo: [context rotation") {
			t.Fatalf("handoff note leaked into the message stream: %q", m.Text)
		}
	}
}

// TestContextMeasuredAtLastCallNotTurnSum: the result event's usage is summed
// across a turn's API steps — every step rereads the cached prefix, so a long
// tool-using turn's sum reaches multiples of the window while the real
// occupancy stays low (#94). Eight steps of 30k against haiku's 200k window
// sum to 240k (120%, past every threshold) yet occupy 15%: the loop must not
// rotate, and the turn must record the last call's measure, not the sum.
func TestContextMeasuredAtLastCallNotTurnSum(t *testing.T) {
	workspace := workspaceWithScript(t, "!ctx 30000 !steps 8\n")
	s := startServer(t, t.TempDir())
	s.createLoop("stepper", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
		"model":          "haiku",
	})

	s.message("stepper", "long tool turn")
	worked := s.waitTurn("stepper", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "long tool turn")
	})
	if worked.ContextTokens != 30000 {
		t.Fatalf("context_tokens = %d, want the last call's 30000, not the turn's sum", worked.ContextTokens)
	}

	// were the sum read as occupancy, the rotation would take this quiet
	// boundary; give it the chance and insist the loop stays put
	if s.hasEvent("stepper", "context_rotated", 3*time.Second) {
		t.Fatal("loop rotated on the turn's summed usage")
	}
	s.message("stepper", "still here")
	stayed := s.waitTurn("stepper", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "still here")
	})
	if stayed.SessionID != worked.SessionID {
		t.Fatalf("second turn moved to session %s; a 15%% context had no reason to rotate", stayed.SessionID)
	}
}

// TestContextRotationForcedBeforeQueuedWork: past the force ceiling the
// rotation stops waiting for quiet — work queued behind a hot context is
// answered only after the handoff, on the fresh session (ADR-0022). Every
// turn hangs 2s at 150k of the 200k window (75%, past the 70% force default),
// so a message sent during a turn is reliably queued behind a forced context.
func TestContextRotationForcedBeforeQueuedWork(t *testing.T) {
	workspace := workspaceWithScript(t, "!ctx 150000 !hang 2\n")
	s := startServer(t, t.TempDir())
	s.createLoop("presser", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
		"model":          "haiku",
	})

	s.message("presser", "one")
	time.Sleep(500 * time.Millisecond) // let a turn start its hang
	s.message("presser", "two")        // queues behind the hot context

	handoff := s.waitTurn("presser", 60*time.Second, func(tr turn) bool {
		return tr.Trigger == "rotation"
	})
	if !s.hasEvent("presser", "context_rotated", 30*time.Second) {
		t.Fatal("forced rotation was not recorded as a spool event")
	}
	// the queued work was not delivered to the hot session the handoff retired
	s.waitTurn("presser", 60*time.Second, func(tr turn) bool {
		return tr.Trigger == "message" && tr.SessionID != handoff.SessionID
	})
}

// TestContextRotationSurvivesServerRestart: a loop that rotates and then goes
// quiet must not resurrect the retired session when the orchestrator
// restarts — the cleared session id is persisted at rotation time, not at the
// next wake (ADR-0022; found in manual testing, 2026-08-21).
func TestContextRotationSurvivesServerRestart(t *testing.T) {
	workspace := workspaceWithScript(t, "!ctx 100000\n")
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("sleeper", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
		"model":          "haiku",
	})

	s.message("sleeper", "fill it")
	retired := s.waitTurn("sleeper", 30*time.Second, func(tr turn) bool {
		return tr.Trigger == "rotation"
	})
	if !s.hasEvent("sleeper", "context_rotated", 30*time.Second) {
		t.Fatal("rotation was not recorded as a spool event")
	}
	s.waitState("sleeper", "asleep", 30*time.Second)
	s.stop()

	s2 := startServer(t, dataDir)
	s2.message("sleeper", "after restart")
	answered := s2.waitTurn("sleeper", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "after restart")
	})
	if answered.SessionID == retired.SessionID {
		t.Fatalf("restart resurrected the retired session %s", retired.SessionID)
	}
}

// TestRotationNoteSurvivesServerRestart: the rotation completed, and the note
// it captured is owed to a session whose first turn has not run yet. Restart
// in that window and the note used to be dropped — the fresh session opened
// with the "could not be resumed" preamble, which is not what happened to a
// loop that retired its own session on purpose (#66, ADR-0022).
func TestRotationNoteSurvivesServerRestart(t *testing.T) {
	workspace := workspaceWithScript(t, "!ctx 100000\n")
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("keeper", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
		"model":          "haiku",
	})

	s.message("keeper", "fill it")
	handoff := s.waitTurn("keeper", 30*time.Second, func(tr turn) bool {
		return tr.Trigger == "rotation" && strings.Contains(tr.ResultText, "handoff note")
	})
	if !s.hasEvent("keeper", "context_rotated", 30*time.Second) {
		t.Fatal("rotation was not recorded as a spool event")
	}
	// the restart lands between the rotation and the fresh session's first
	// turn: the note is captured, nothing has consumed it
	s.waitState("keeper", "asleep", 30*time.Second)
	s.stop()

	s2 := startServer(t, dataDir)
	s2.message("keeper", "after restart")
	carried := s2.waitTurn("keeper", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "after restart")
	})
	if carried.SessionID == handoff.SessionID {
		t.Fatalf("restart resurrected the retired session %s", handoff.SessionID)
	}
	if !strings.Contains(carried.ResultText, "your context was rotated") ||
		!strings.Contains(carried.ResultText, "Handoff note from your previous session") {
		t.Fatalf("the note did not survive the restart:\n%s", carried.ResultText)
	}
	if strings.Contains(carried.ResultText, "could not be resumed") {
		t.Fatalf("a deliberate rotation was reported to the loop as a lost session:\n%s", carried.ResultText)
	}
}

// TestRotationIntentSurvivesServerRestart: once the handoff turn is asked
// for, that session has been told it ends here. A shutdown before the
// rotation lands used to leave the intent in actor memory only, so the
// restarted server resumed the session its own last turn retired and paid for
// the handoff twice (#66). The handoff turn hangs, so the shutdown reliably
// falls inside that window.
func TestRotationIntentSurvivesServerRestart(t *testing.T) {
	// every turn reports 50% (past the 40% arm default) and hangs; the hang
	// is what makes the shutdown land inside the handoff turn rather than
	// racing it
	workspace := workspaceWithScript(t, "!ctx 100000 !hang 5\n")
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("interrupted", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
		"model":          "haiku",
	})

	s.message("interrupted", "fill it")
	retired := s.waitRunningTurn("interrupted", 30*time.Second, func(tr turn) bool {
		return tr.Trigger == "rotation"
	})
	s.stop()

	s2 := startServer(t, dataDir)
	s2.message("interrupted", "after restart")
	// a hung turn answers "hung 5s" rather than echoing, so the work after
	// the restart is identified by when it ran, not by what it said
	answered := s2.waitTurn("interrupted", 60*time.Second, func(tr turn) bool {
		return tr.Trigger == "message" && tr.StartedAt > retired.StartedAt
	})
	if answered.SessionID == retired.SessionID {
		t.Fatalf("restart resumed session %s, whose last turn was told it ends here", retired.SessionID)
	}
	for _, tr := range s2.turns("interrupted") {
		if tr.StartedAt > retired.StartedAt && tr.SessionID == retired.SessionID {
			t.Fatalf("turn %s ran on the retired session after the restart", tr.ID)
		}
	}
}

// TestContextRotationSurvivesHandoffCrash: ADR-0022's fallback — a handoff
// turn that dies with its process still rotates, just without a note, and
// later work is answered on the fresh session.
func TestContextRotationSurvivesHandoffCrash(t *testing.T) {
	// turn 1 arms at 75%; turn 2 — the handoff request — crashes mid-turn
	workspace := workspaceWithScript(t, "!ctx 150000\n!crash\n")
	s := startServer(t, t.TempDir())
	s.createLoop("crasher", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
		"model":          "haiku",
	})

	s.message("crasher", "boom")
	failed := s.waitTurn("crasher", 30*time.Second, func(tr turn) bool {
		return tr.Trigger == "rotation" && tr.IsError
	})
	if !s.hasEvent("crasher", "context_rotated", 30*time.Second) {
		t.Fatal("a crashed handoff turn must still rotate")
	}

	s.message("crasher", "still alive?")
	s.waitTurn("crasher", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "still alive?") && tr.SessionID != failed.SessionID
	})
}

// A message too big for the model's window comes back as an ordinary errored
// turn saying "Prompt is too long" (verified by make e2e-context). The loop
// must call that what it is — a payload that will never fit — rather than
// filing it with every other model error, and must carry on afterwards.
func TestOverWindowMessageIsRecordedAsTooLong(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("oversize", map[string]any{
		"workspace_path": workspaceWithScript(t, "first turn fits\n!toolong"),
		"workspace_mode": "dir",
	})
	first := s.waitTurn("oversize", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "first turn fits")
	})

	s.message("oversize", "a wall of text")
	failed := s.waitTurn("oversize", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "Prompt is too long")
	})
	if !failed.IsError {
		t.Fatal("an over-window turn must be recorded as errored")
	}
	if !s.hasEvent("oversize", "message_too_long", 10*time.Second) {
		t.Fatal("no message_too_long event: the failure is indistinguishable from any model error")
	}
	if s.hasEvent("oversize", "crash", 2*time.Second) {
		t.Fatal("an over-window turn is not a crash")
	}
	if failed.SessionID != first.SessionID {
		t.Fatalf("session changed on an over-window turn: %s -> %s", first.SessionID, failed.SessionID)
	}
}

// TestManualRotation: the operator's rotate control runs the ADR-0022
// handoff flow on demand — the loop writes its note now and continues on a
// fresh session seeded from it, with no fill threshold involved.
func TestManualRotation(t *testing.T) {
	ws := workspaceWithScript(t, "!ctx 0\nhandoff note from the old self\n")
	s := startServer(t, t.TempDir())
	s.createLoop("aster", map[string]any{"workspace_path": ws})
	s.waitTurn("aster", 20*time.Second, func(tn turn) bool { return tn.Trigger == "tick" })

	s.mustJSON("POST", "/api/loops/aster/rotate", nil, nil)
	handoff := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return tn.Trigger == "rotation" && strings.Contains(tn.ResultText, "handoff note from the old self")
	})

	s.message("aster", "carry on fresh")
	fresh := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "carry on fresh")
	})
	if fresh.SessionID == handoff.SessionID {
		t.Fatalf("work after the rotation stayed on the retired session %s", handoff.SessionID)
	}
	if !strings.Contains(fresh.ResultText, "your context was rotated") ||
		!strings.Contains(fresh.ResultText, "handoff note from the old self") {
		t.Fatalf("fresh session missing the rotation preamble or note: %s", fresh.ResultText)
	}
}

// A rotate request while a turn is running must not interrupt it: the turn
// completes and the handoff takes the following quiet boundary.
func TestManualRotationWaitsForTheRunningTurn(t *testing.T) {
	ws := workspaceWithScript(t, "!ctx 0\n!hang 3\nlate handoff note\n")
	s := startServer(t, t.TempDir())
	s.createLoop("aster", map[string]any{"workspace_path": ws})
	s.waitTurn("aster", 20*time.Second, func(tn turn) bool { return tn.Trigger == "tick" })

	s.message("aster", "start hanging")
	s.waitState("aster", "busy", 10*time.Second)
	s.mustJSON("POST", "/api/loops/aster/rotate", nil, nil)

	hang := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "hung 3s")
	})
	handoff := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return tn.Trigger == "rotation" && strings.Contains(tn.ResultText, "late handoff note")
	})
	if handoff.StartedAt < hang.EndedAt {
		t.Fatalf("handoff started at %d, before the running turn ended at %d", handoff.StartedAt, hang.EndedAt)
	}
}

// A loop that cannot run at all must back off further every time, across
// the rotation its failures trigger. Rotating used to re-floor the ladder,
// so a broken loop respawned every few seconds forever and blamed a fresh
// session each round (#89).
func TestRetryLadderSurvivesAFailureRotation(t *testing.T) {
	workspace := t.TempDir()
	s := startServer(t, t.TempDir())
	s.createLoop("broken", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
	})

	s.message("broken", "first thing")
	s.waitTurn("broken", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "first thing")
	})
	s.waitState("broken", "asleep", 30*time.Second)

	// from here nothing can run: neither this session nor a fresh one
	if err := os.WriteFile(filepath.Join(workspace, ".fakeclaude-resume-broken"), []byte("all"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.message("broken", "second thing")
	if !s.hasEvent("broken", "session_unusable", 60*time.Second) {
		t.Fatal("the unresumable session was never rotated away")
	}

	// The first dead resume waits 10s. The second rotates the session away
	// and the spawn after that fails too — its wait must be 20s, the next
	// rung. Re-flooring showed up here as a second 10s.
	waits := waitForRetries(t, s, "broken", 2, 90*time.Second)
	want := []int64{10000, 20000}
	for i, ms := range want {
		if waits[i] != ms {
			t.Fatalf("retry waits %v, want %v — the ladder reset instead of climbing", waits, want)
		}
	}
}

// waitForRetries collects the delays a loop scheduled its retries with, in
// order, until it has n of them.
func waitForRetries(t *testing.T, s *server, name string, n int, timeout time.Duration) []int64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var events []spoolEvent
		s.mustJSON("GET", "/api/loops/"+name+"/events?limit=500", nil, &events)
		var waits []int64
		for _, e := range events { // oldest first, as the endpoint returns them
			if e.Subtype != "retry_scheduled" {
				continue
			}
			var p struct {
				InMS int64 `json:"in_ms"`
			}
			if err := json.Unmarshal([]byte(e.Payload), &p); err == nil {
				waits = append(waits, p.InMS)
			}
		}
		if len(waits) >= n {
			return waits
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("loop %s scheduled fewer than %d retries", name, n)
	return nil
}
