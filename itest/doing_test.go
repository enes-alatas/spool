//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// waitDoing polls the loop record until its doing reads want.
func (s *server) waitDoing(name, want string) loopView {
	s.t.Helper()
	var view loopView
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if view = s.loop(name); view.Doing == want {
			return view
		}
	}
	s.t.Fatalf("%s's doing = %q, want %q", name, view.Doing, want)
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
	s := startServer(t, t.TempDir())
	s.createLoop("alpha", map[string]any{"workspace_path": ws})

	set := s.waitDoing("alpha", "implementing #123")
	if set.DoingAt == 0 {
		t.Fatalf("a doing with no time set: %+v", set)
	}

	s.message("alpha", "how is it going")
	s.waitTurn("alpha", 10*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "still on it") })
	if kept := s.loop("alpha"); kept.Doing != "implementing #123" || kept.DoingAt != set.DoingAt {
		t.Fatalf("after a reply with no doing: %q at %d, want %q kept at %d", kept.Doing, kept.DoingAt, set.Doing, set.DoingAt)
	}

	s.message("alpha", "and now")
	s.waitDoing("alpha", strings.Repeat("x", 60))

	s.message("alpha", "wrap up")
	s.waitTurn("alpha", 10*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "done for now") })
	s.waitDoing("alpha", "")
}
