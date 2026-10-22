package cloud

import (
	"testing"
	"time"
)

// A turn shows from its send until its Stop post. Cut off, it shows until
// its quiet bound, and a later turn's send leaves it there until that turn
// is cut off too, which takes its place. A link off claude.ai never shows.
func TestATurnShowsUntilItsPostOrItsBound(t *testing.T) {
	cloud := &Runtime{Dir: t.TempDir()}
	const first, second = sessionPages + "session_01First", sessionPages + "session_01Second"
	inSession := func(url string) {
		t.Helper()
		if err := cloud.update("loop-1", func(current *state) { current.SessionURL = url }); err != nil {
			t.Fatal(err)
		}
	}
	shown := func() (Turn, bool) { return cloud.Turn("loop-1") }

	if turn, ok := shown(); ok {
		t.Fatalf("a loop that sent nothing shows %+v", turn)
	}
	inSession(first)
	sent := time.Now()
	running := cloud.beginTurn("loop-1", sent, time.Hour)
	if turn, ok := shown(); !ok || turn != (Turn{Since: sent.UnixMilli(), Until: sent.Add(time.Hour).UnixMilli(), SessionURL: first}) {
		t.Fatalf("the running turn shows %+v (%v)", turn, ok)
	}
	cloud.endTurn("loop-1", running.Since)
	if turn, ok := shown(); ok {
		t.Fatalf("a turn whose post came shows %+v", turn)
	}

	cutOff := cloud.beginTurn("loop-1", time.Now(), time.Hour)
	cloud.cutOffTurn("loop-1", cutOff)
	cloud.endTurn("loop-1", cutOff.Since)
	if turn, ok := shown(); !ok || !turn.CutOff || turn.SessionURL != first {
		t.Fatalf("the cut-off turn shows %+v (%v), want it cut off in the first session", turn, ok)
	}

	inSession(second)
	later := cloud.beginTurn("loop-1", time.Now().Add(time.Millisecond), time.Hour)
	cloud.endTurn("loop-1", later.Since)
	if turn, _ := shown(); turn.Since != cutOff.Since || turn.SessionURL != first {
		t.Errorf("a later turn's send and post replaced the cut-off one: %+v", turn)
	}
	later = cloud.beginTurn("loop-1", time.Now().Add(2*time.Millisecond), time.Hour)
	cloud.cutOffTurn("loop-1", later)
	if turn, _ := shown(); turn.Since != later.Since || turn.SessionURL != second || !turn.CutOff {
		t.Errorf("the later cut-off turn shows as %+v, want it in the second session", turn)
	}

	if err := cloud.update("loop-1", func(current *state) { current.Turn.Until = time.Now().Add(-time.Second).UnixMilli() }); err != nil {
		t.Fatal(err)
	}
	if turn, ok := shown(); ok {
		t.Errorf("a turn past its bound shows %+v", turn)
	}
	inSession("https://example.test/session_01Elsewhere")
	cloud.beginTurn("loop-1", time.Now(), time.Hour)
	if turn, ok := shown(); ok {
		t.Errorf("a turn linked off claude.ai shows %+v", turn)
	}
}
