//go:build integration

package itest

import (
	"math"
	"strings"
	"testing"
	"time"
)

// fakeCostPerTurn mirrors cmd/fakeclaude's per-turn increment. The fake
// reports a session running total, as the real CLI does under --resume.
const fakeCostPerTurn = 0.001

func aboutEqual(got, want float64) bool {
	return math.Abs(got-want) < fakeCostPerTurn/100
}

// costShape is what the turns of one session should add up to: each turn
// priced at the increment, and the deltas summing to the running total the
// last turn reported. Asserted over however many turns the loop took —
// creation fires a tick of its own, and a test that counted turns instead
// would be asserting the trigger schedule rather than the arithmetic.
func costShape(t *testing.T, s *server, loop string) (sum, sessionTotal float64) {
	t.Helper()
	done := s.completed(loop)
	if len(done) < 2 {
		t.Fatalf("want at least 2 completed turns to compare, got %d", len(done))
	}
	session := done[0].SessionID
	for _, tn := range done {
		if tn.SessionID != session {
			t.Fatalf("turns span sessions (%s, %s), so no running total connects them",
				session, tn.SessionID)
		}
		if !aboutEqual(tn.CostUSD, fakeCostPerTurn) {
			t.Errorf("turn cost %v, want the per-turn %v: %s", tn.CostUSD, fakeCostPerTurn, dump(tn))
		}
		sum += tn.CostUSD
		sessionTotal = math.Max(sessionTotal, tn.SessionCostUSD)
	}
	return sum, sessionTotal
}

// TestTurnCostIsPerTurnNotSessionTotal: cost_usd prices the turn, so summing
// the column prices the work. The CLI hands us the session's running total,
// and storing that verbatim made every sum overstate spend by as much as an
// order of magnitude (#191) — the control room's cost figures are such a sum.
func TestTurnCostIsPerTurnNotSessionTotal(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("spender", nil)

	s.message("spender", "first")
	s.waitTurn("spender", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "first")
	})
	s.message("spender", "second")
	s.waitTurn("spender", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "second")
	})

	sum, sessionTotal := costShape(t, s, "spender")
	if !aboutEqual(sum, sessionTotal) {
		t.Errorf("turn costs sum to %v against a session total of %v", sum, sessionTotal)
	}
	// The loop view sums the column, which is the reader the bug was found in.
	if got := s.loop("spender").CostToday; !aboutEqual(got, sessionTotal) {
		t.Errorf("cost_today_usd %v, want the session's %v", got, sessionTotal)
	}
}

// TestTurnCostSurvivesRestartMidSession: the previous total is read from the
// store, not remembered. An actor that kept it in memory would price the
// first turn after a restart at the whole session's spend — the same bug the
// column was fixed for, rarer and harder to see.
func TestTurnCostSurvivesRestartMidSession(t *testing.T) {
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("resumer", nil)

	s.message("resumer", "before restart")
	before := s.waitTurn("resumer", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "before restart")
	})
	s.stop()

	s2 := startServer(t, dataDir)
	s2.message("resumer", "after restart")
	after := s2.waitTurn("resumer", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "after restart")
	})

	if after.SessionID != before.SessionID {
		t.Fatalf("restart minted session %s instead of resuming %s, so no total carried over",
			after.SessionID, before.SessionID)
	}
	if !aboutEqual(after.CostUSD, fakeCostPerTurn) {
		t.Errorf("turn after restart cost %v, want the per-turn %v — priced against a lost baseline",
			after.CostUSD, fakeCostPerTurn)
	}
	if sum, sessionTotal := costShape(t, s2, "resumer"); !aboutEqual(sum, sessionTotal) {
		t.Errorf("across the restart, turn costs sum to %v against a session total of %v",
			sum, sessionTotal)
	}
}
