//go:build integration

package itest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setPlanUsage has fakeclaude's /usage print a session at percent used,
// resetting at resets, in the CLI's own words, and no week line.
func setPlanUsage(t *testing.T, hub *server, percent int, resets time.Time) {
	t.Helper()
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	local := resets.In(berlin)
	table := fmt.Sprintf("You are currently using your subscription to power your Claude Code usage\n\n"+
		"Current session: %d%% used · resets %s at %s (Europe/Berlin)\n", percent, local.Format("Jan 2"), strings.ToLower(local.Format("3:04PM")))
	if err := os.WriteFile(filepath.Join(hub.fkState, "usage.txt"), []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
}

// planUsageReads counts the reads of the account's plan usage.
func planUsageReads(t *testing.T, hub *server) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(hub.fkState, "usage-reads"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "read\n")
}

// A login whose loops all run in the cloud reports its plan usage through
// the hub's run of the CLI's /usage after a cloud turn (#1285): the
// reading is kept as the usage command's, and over the threshold the cap sleeps
// the cloud loop until the window resets, as it does a contained loop. A
// turn ending within five minutes of the read reads nothing more.
func TestACloudOnlyLoginReportsItsPlanUsage(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	// the CLI answers only on the login's token, so a reading proves /usage
	// ran on it
	if err := os.WriteFile(filepath.Join(hub.fkState, "admitted-token"), []byte(cloudFixtureToken), 0o600); err != nil {
		t.Fatal(err)
	}
	if view := hub.planUsage(); view.Unknown == "" {
		t.Fatalf("before any cloud turn: %+v, want unknown", view)
	}
	resets := time.Now().Add(4 * time.Hour).Truncate(time.Minute)
	setPlanUsage(t, hub, 95, resets)

	hub.message("cirrus", "spend")
	hub.waitTurn("cirrus", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "spend") })
	var view planUsageJSON
	within(t, 15*time.Second, "the read after the turn", func() bool {
		view = hub.planUsage()
		return view.Source == "usage_command"
	})
	if view.FiveHour == nil || view.FiveHour.UsedPercent != 95 || view.FiveHour.ResetsAt != resets.UnixMilli() || view.SevenDay != nil {
		t.Fatalf("plan usage = %+v %+v, want the five-hour window at 95%%, resetting at %d", view.FiveHour, view.SevenDay, resets.UnixMilli())
	}
	hub.waitState("cirrus", "capped", 15*time.Second)
	if view := hub.planUsage(); view.Cap == nil || view.Cap.Until != resets.UnixMilli() {
		t.Fatalf("cap = %+v, want the five-hour window's, until %d", view.Cap, resets.UnixMilli())
	}

	hub.mustJSON("POST", "/api/plan-cap/resume", nil, nil)
	setPlanUsage(t, hub, 99, resets)
	hub.message("cirrus", "again")
	hub.waitTurn("cirrus", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "again") })
	time.Sleep(2 * time.Second)
	if reads := planUsageReads(t, hub); reads != 1 {
		t.Fatalf("%d reads of the plan usage, want 1: the second turn ended within five minutes of the first read", reads)
	}
	if view := hub.planUsage(); view.FiveHour == nil || view.FiveHour.UsedPercent != 95 {
		t.Fatalf("after the second turn: %+v, want the first reading's 95%%", view.FiveHour)
	}
}
