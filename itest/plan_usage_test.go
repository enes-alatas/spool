//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

type planWindowJSON struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at"`
	Reset       bool    `json:"reset"`
	CapPercent  int     `json:"cap_percent"`
}

type planUsageJSON struct {
	FiveHour *planWindowJSON `json:"five_hour"`
	SevenDay *planWindowJSON `json:"seven_day"`
	AsOf     int64           `json:"as_of"`
	Source   string          `json:"source"`
	Unknown  string          `json:"unknown"`
	Cap      *struct {
		Windows []string `json:"windows"`
		Until   int64    `json:"until"`
	} `json:"cap"`
	ResumedUntil int64 `json:"resumed_until"`
}

func (s *server) planUsage() planUsageJSON {
	s.t.Helper()
	var view planUsageJSON
	s.mustJSON("GET", "/api/plan-usage", nil, &view)
	return view
}

// The hub reads the Claude plan's usage from the rate-limit event a loop's
// own claude process emits (#647): unknown, with the reason, until a turn
// carries one; then both windows, as percent with their reset times; kept
// through a turn that carries none, and through a restart of the hub.
func TestThePlanUsageIsReadFromTheLoopsStream(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	srv := startServer(t, dataDir)
	// line 1 answers the creation tick with no rate-limit event; line 2
	// reports the windows; line 3 is a turn with no event again
	ws := workspaceWithScript(t, "first\n!usage 0.42 0.17 measured\nquiet\n")
	srv.createLoop("meter", map[string]any{"workspace_path": ws})
	srv.waitTurn("meter", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "first") })

	if view := srv.planUsage(); view.Unknown == "" || view.FiveHour != nil || view.SevenDay != nil || view.AsOf != 0 {
		t.Fatalf("before any rate-limit event the read must be unknown with a reason, got %+v", view)
	}

	before := time.Now().UnixMilli()
	srv.message("meter", "how much is used?")
	srv.waitTurn("meter", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "measured") })
	measured := srv.planUsage()
	assertPlanUsage(t, measured, before)

	srv.message("meter", "and now?")
	srv.waitTurn("meter", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "quiet") })
	if view := srv.planUsage(); view.AsOf != measured.AsOf || *view.FiveHour != *measured.FiveHour {
		t.Fatalf("a turn with no rate-limit event changed the read: %+v, was %+v", view, measured)
	}

	srv.stop()
	srv = startServer(t, dataDir)
	if view := srv.planUsage(); view.AsOf != measured.AsOf || view.FiveHour == nil || *view.SevenDay != *measured.SevenDay {
		t.Fatalf("the read did not survive a restart: %+v, was %+v", view, measured)
	}
}

// assertPlanUsage checks a read of fakeclaude's "!usage 0.42 0.17", which
// reports the five-hour window resetting in five hours and the seven-day one
// in seven days, observed no earlier than since.
func assertPlanUsage(t *testing.T, view planUsageJSON, since int64) {
	t.Helper()
	if view.Unknown != "" || view.Source != "stream" || view.AsOf < since {
		t.Fatalf("read = %+v; want a stream observation from after %d", view, since)
	}
	near := func(window *planWindowJSON, percent float64, resetIn time.Duration) bool {
		if window == nil || window.Reset {
			return false
		}
		want := since + resetIn.Milliseconds()
		return window.UsedPercent > percent-0.01 && window.UsedPercent < percent+0.01 &&
			window.ResetsAt > want-time.Minute.Milliseconds() && window.ResetsAt < want+time.Minute.Milliseconds()
	}
	if !near(view.FiveHour, 42, 5*time.Hour) {
		t.Fatalf("five_hour = %+v; want 42%% resetting in five hours", view.FiveHour)
	}
	if !near(view.SevenDay, 17, 7*24*time.Hour) {
		t.Fatalf("seven_day = %+v; want 17%% resetting in seven days", view.SevenDay)
	}
}
