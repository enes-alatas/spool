//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
)

// A loop at its own daily USD cap sleeps at its next quiet boundary until
// the hub's local day turns, and only that loop (#652): the turn that
// reached the cap finishes, a message waits, a loop beside it with no cap
// keeps working, and raising the cap wakes the loop with what waited.
// fakeclaude's turns cost 0.001 each, so a cap of 0.0015 holds after two.
func TestALoopAtItsDailyCapSleepsUntilTheCapIsRaised(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	hub.createLoop("frugal", map[string]any{"daily_cap_usd": 0.0015,
		"workspace_path": workspaceWithScript(t, "first\nsecond\nheld\n")})
	hub.createLoop("free", map[string]any{"workspace_path": workspaceWithScript(t, "first\nsecond\nthird\n")})
	for _, name := range []string{"frugal", "free"} {
		hub.waitTurn(name, firstTurnWait, func(done turn) bool { return strings.Contains(done.ResultText, "first") })
	}
	if view := hub.loop("frugal"); view.DailyCapReached || view.DailyCapUSD != 0.0015 {
		t.Fatalf("after one turn: daily_cap_reached %v, daily_cap_usd %v; want under a cap of 0.0015", view.DailyCapReached, view.DailyCapUSD)
	}

	hub.message("frugal", "spend")
	hub.waitTurn("frugal", 30*time.Second, func(done turn) bool { return strings.Contains(done.ResultText, "second") })
	hub.waitState("frugal", "capped", 15*time.Second)
	_, next := loop.LocalDay(time.Now()) // the hub runs in the test's own zone
	tomorrow := next.UnixMilli()
	if view := hub.loop("frugal"); !view.DailyCapReached || view.CappedUntil != tomorrow {
		t.Fatalf("at the cap: daily_cap_reached %v, capped_until %d; want reached, until the next local day at %d",
			view.DailyCapReached, view.CappedUntil, tomorrow)
	}

	hub.message("frugal", "while capped")
	hub.message("free", "go on")
	hub.waitTurn("free", 30*time.Second, func(done turn) bool { return strings.Contains(done.ResultText, "second") })
	hub.noTurn(2*time.Second, map[string]string{"frugal": "held"})

	var raised loopView
	hub.mustJSON("PATCH", "/api/loops/frugal", map[string]any{"daily_cap_usd": 1}, &raised)
	hub.waitTurn("frugal", 30*time.Second, func(done turn) bool { return strings.Contains(done.ResultText, "held") })
	if view := hub.loop("frugal"); view.DailyCapReached || view.State == "capped" {
		t.Fatalf("after raising the cap: daily_cap_reached %v, state %s", view.DailyCapReached, view.State)
	}

	refusedSaying(t, hub, "PATCH", "/api/loops/frugal", map[string]any{"daily_cap_usd": -1}, "daily_cap_usd must be")
	refusedSaying(t, hub, "POST", "/api/loops", map[string]any{"name": "lavish", "mission": "m", "daily_cap_usd": 1e6}, "daily_cap_usd must be")
}

// A cloud loop's daily cap counts the hub's estimate of its turns' cost
// (#652). The fake's one priced turn is estimated at 0.00088, over a cap
// of 0.0005.
func TestACloudLoopsDailyCapCountsTheEstimate(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	hub.mustJSON("PATCH", "/api/loops/cirrus", map[string]any{"daily_cap_usd": 0.0005}, nil)
	hub.message("cirrus", "priced words")
	hub.waitTurn("cirrus", 30*time.Second, func(done turn) bool { return strings.Contains(done.ResultText, "priced words") })
	hub.waitState("cirrus", "capped", 15*time.Second)
	if view := hub.loop("cirrus"); !view.DailyCapReached {
		t.Fatalf("after an estimated 0.00088 under a cap of 0.0005: daily_cap_reached %v, state %s", view.DailyCapReached, view.State)
	}
}
