//go:build integration

package itest

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// noTurn fails if any loop named in replies takes a turn whose reply
// contains the text given for it, checked across the whole window and at
// least once: a capped loop must stay asleep, and only waiting shows it.
func (s *server) noTurn(window time.Duration, replies map[string]string) {
	s.t.Helper()
	deadline := time.Now().Add(window)
	for {
		for loop, text := range replies {
			for _, tr := range s.turns(loop) {
				if strings.Contains(tr.ResultText, text) {
					s.t.Fatalf("%s took a turn while capped: %+v", loop, tr)
				}
			}
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// Above the plan cap every loop sleeps at its next quiet boundary
// (ADR-0047): unknown usage caps nothing; the turn whose usage crossed a
// threshold finishes and the message queued behind it waits; a loop that
// was asleep is capped too and keeps what it is told; and raising the
// threshold wakes both with what waited.
func TestThePlanCapSleepsTheFleetUntilAThresholdIsRaised(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	srv.createLoop("spender", map[string]any{"workspace_path": workspaceWithScript(t,
		"first\n!usage 0.95 0.3 !hang 3\nheld\n")})
	srv.createLoop("bystander", map[string]any{"workspace_path": workspaceWithScript(t, "first\nlater\n")})
	for _, name := range []string{"spender", "bystander"} {
		srv.waitTurn(name, 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "first") })
	}
	if view := srv.planUsage(); view.Cap != nil {
		t.Fatalf("unknown usage capped the fleet: %+v", view.Cap)
	}

	srv.message("spender", "spend")
	srv.waitRunningTurn("spender", 30*time.Second, func(turn) bool { return true })
	srv.message("spender", "and again")
	srv.waitTurn("spender", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "hung 3s") })
	srv.waitState("spender", "capped", 15*time.Second)
	srv.waitState("bystander", "capped", 15*time.Second)

	view := srv.planUsage()
	fiveHoursOut := time.Now().Add(5 * time.Hour).UnixMilli()
	if view.Cap == nil || !slices.Equal(view.Cap.Windows, []string{"five_hour"}) ||
		view.Cap.Until < fiveHoursOut-time.Minute.Milliseconds() || view.Cap.Until > fiveHoursOut {
		t.Fatalf("cap = %+v; want the five-hour window, until its reset five hours out", view.Cap)
	}
	if view.FiveHour == nil || view.FiveHour.CapPercent != 90 || view.SevenDay.CapPercent != 90 {
		t.Fatalf("windows = %+v %+v; want each to carry the default 90%% threshold", view.FiveHour, view.SevenDay)
	}
	if until := srv.loop("bystander").CappedUntil; until != view.Cap.Until {
		t.Fatalf("bystander capped_until = %d; want the cap's %d", until, view.Cap.Until)
	}

	srv.message("bystander", "while capped")
	srv.noTurn(3*time.Second, map[string]string{"spender": "held", "bystander": "later"})

	if resp, body := srv.do("PUT", "/api/settings", map[string]any{"plan_cap_five_hour_percent": 101}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a threshold of 101 answered %d %s; want 400", resp.StatusCode, body)
	}
	srv.mustJSON("PUT", "/api/settings", map[string]any{"plan_cap_five_hour_percent": 96}, nil)
	srv.waitTurn("spender", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "held") })
	srv.waitTurn("bystander", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "later") })
	if view := srv.planUsage(); view.Cap != nil || view.FiveHour.CapPercent != 96 {
		t.Fatalf("after raising the threshold: cap %+v, five_hour %+v", view.Cap, view.FiveHour)
	}
}

// A capped loop wakes on its own once the window over its threshold
// resets, and a Resume now wakes it at once without touching the
// thresholds (ADR-0047).
func TestThePlanCapLiftsAtTheResetAndOnResumeNow(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	srv.createLoop("spender", map[string]any{"workspace_path": workspaceWithScript(t,
		"first\n!usage 0.95 0.3 resets=8 over\nwoke\n!usage 0.95 0.3 over again\nresumed\n")})
	srv.waitTurn("spender", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "first") })

	srv.message("spender", "spend")
	srv.waitTurn("spender", 30*time.Second, func(tr turn) bool { return tr.ResultText == "over" })
	srv.waitState("spender", "capped", 15*time.Second)
	until := srv.loop("spender").CappedUntil
	srv.message("spender", "after the reset")
	woke := srv.waitTurn("spender", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "woke") })
	if woke.StartedAt < until {
		t.Fatalf("the loop woke at %d, before the reset at %d", woke.StartedAt, until)
	}

	srv.message("spender", "spend again")
	srv.waitTurn("spender", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "over again") })
	srv.waitState("spender", "capped", 15*time.Second)
	srv.message("spender", "resume")
	srv.noTurn(2*time.Second, map[string]string{"spender": "resumed"})

	var resumed planUsageJSON
	srv.mustJSON("POST", "/api/plan-cap/resume", nil, &resumed)
	if resumed.Cap != nil || resumed.ResumedUntil < time.Now().Add(4*time.Hour).UnixMilli() {
		t.Fatalf("Resume now answered cap %+v, resumed_until %d; want no cap, held to the five-hour reset", resumed.Cap, resumed.ResumedUntil)
	}
	srv.waitTurn("spender", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "resumed") })
	if settings := srv.settingsThresholds(); settings[0] != 90 || settings[1] != 90 {
		t.Fatalf("Resume now changed the thresholds to %v", settings)
	}
}

// settingsThresholds reads the plan cap's two thresholds from settings.
func (s *server) settingsThresholds() [2]int {
	s.t.Helper()
	var view struct {
		FiveHour int `json:"plan_cap_five_hour_percent"`
		SevenDay int `json:"plan_cap_seven_day_percent"`
	}
	s.mustJSON("GET", "/api/settings", nil, &view)
	return [2]int{view.FiveHour, view.SevenDay}
}
