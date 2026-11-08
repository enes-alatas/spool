//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// waitDoing polls the loop record until its doing reads want.
func (hub *server) waitDoing(name, want string) loopView {
	hub.t.Helper()
	var view loopView
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if view = hub.loop(name); view.Doing == want {
			return view
		}
	}
	hub.t.Fatalf("%s's doing = %q, want %q", name, view.Doing, want)
	return view
}

// A loop says what it is working on with a [doing: …] trailer, and the
// hub keeps the line on the loop record until the loop replaces it (#751):
// a reply without one keeps it and its age, a longer one is cut at 60
// characters, and an empty one clears it.
func TestALoopSetsItsDoingWithATrailer(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 70)
	ws := workspaceWithScript(t, "starting [next-wake: 1h] [doing: implementing #123]\n"+
		"still on it\n"+
		"[doing: "+long+"]\n"+
		"done for now [doing: ]\n")
	hub := startServer(t, t.TempDir())
	hub.createLoop("alpha", map[string]any{"workspace_path": ws})

	set := hub.waitDoing("alpha", "implementing #123")
	if set.DoingAt == 0 {
		t.Fatalf("a doing with no time set: %+v", set)
	}

	hub.message("alpha", "how is it going")
	hub.waitTurn("alpha", 10*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "still on it") })
	if kept := hub.loop("alpha"); kept.Doing != "implementing #123" || kept.DoingAt != set.DoingAt {
		t.Fatalf("after a reply with no doing: %q at %d, want %q kept at %d", kept.Doing, kept.DoingAt, set.Doing, set.DoingAt)
	}

	hub.message("alpha", "and now")
	hub.waitDoing("alpha", strings.Repeat("x", 60))

	hub.message("alpha", "wrap up")
	hub.waitTurn("alpha", 10*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "done for now") })
	hub.waitDoing("alpha", "")
}

// A doing belongs to the session that set it (#1284): within the session
// a reply with no trailer keeps it, and a fresh session, after a rotation,
// starts with none, until it sets one of its own. The script answers each
// session's turns from its first line, so the successor's first reply has
// no trailer.
func TestAFreshSessionStartsWithNoDoing(t *testing.T) {
	t.Parallel()
	ws := workspaceWithScript(t, "hello\n"+
		"on it [doing: before the rotation]\n"+
		"still on it\n"+
		"handing off\n")
	hub := startServer(t, t.TempDir())
	hub.createLoop("alpha", map[string]any{"workspace_path": ws})
	hub.waitTurn("alpha", firstTurnWait, func(turn) bool { return true })

	hub.message("alpha", "start")
	set := hub.waitDoing("alpha", "before the rotation")
	hub.message("alpha", "how is it going")
	hub.waitTurn("alpha", 10*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "still on it") })
	if kept := hub.loop("alpha"); kept.Doing != set.Doing || kept.DoingAt != set.DoingAt {
		t.Fatalf("after a reply with no trailer in the same session: %q at %d, want %q kept at %d", kept.Doing, kept.DoingAt, set.Doing, set.DoingAt)
	}

	old := hub.loop("alpha").CurrentSessionID
	hub.mustJSON("POST", "/api/loops/alpha/rotate", nil, nil)
	within(t, 30*time.Second, "the rotation", func() bool { return hub.loop("alpha").CurrentSessionID != old })
	at := time.Now().UnixMilli()
	hub.message("alpha", "wake on the successor")
	hub.waitTurn("alpha", 30*time.Second, func(tr turn) bool { return tr.EndedAt >= at && tr.SessionID != old })
	if fresh := hub.loop("alpha"); fresh.Doing != "" {
		t.Errorf("the successor session's row shows doing %q, want none until it sets one", fresh.Doing)
	}
}
