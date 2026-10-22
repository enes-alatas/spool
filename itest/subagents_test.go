//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// turnArgs is the argument line of every turn's claude run recordingClaude
// saved under dir, in no order: a run with a session is a turn's, which
// leaves out the hub's alias resolution at start.
func turnArgs(t *testing.T, dir string) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "args.*"))
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string]string{}
	for _, path := range paths {
		line, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(line), "--session-id") || strings.Contains(string(line), "--resume") {
			runs[path] = string(line)
		}
	}
	return runs
}

// A loop set to work alone runs every turn with the Agent tool denied, and
// one that allows subagents again runs its next wake without the denial.
// The setting is on the loop view, and a value that isn't one is refused
// (#855).
func TestALoopWithSubagentsOffIsDeniedTheAgentTool(t *testing.T) {
	t.Parallel()
	runs := t.TempDir()
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--claude-bin", recordingClaude(t, runs))
	hub.createLoop("solo", map[string]any{"subagents": "off"})
	hub.createLoop("crew", nil)
	if solo, crew := hub.loop("solo").Subagents, hub.loop("crew").Subagents; solo != "off" || crew != "allowed" {
		t.Fatalf("subagents = %q and %q, want off and the default, allowed", solo, crew)
	}
	refusedSaying(t, hub, "PATCH", "/api/loops/solo", map[string]string{"subagents": "some"}, "subagents must be")
	refusedSaying(t, hub, "POST", "/api/loops", map[string]any{"name": "few", "mission": "m", "subagents": "3"}, "subagents must be")

	hub.message("solo", "work alone")
	hub.message("crew", "bring help")
	hub.waitTurn("solo", 30*time.Second, func(turn) bool { return true })
	hub.waitTurn("crew", 30*time.Second, func(turn) bool { return true })
	denied := 0
	for _, line := range turnArgs(t, runs) {
		if strings.Contains(line, "--disallowedTools Agent,Task") {
			denied++
		}
	}
	if denied != 1 {
		t.Fatalf("%d of %d turns ran with the Agent tool denied, want solo's alone", denied, len(turnArgs(t, runs)))
	}

	hub.waitState("solo", "asleep", 30*time.Second)
	before := turnArgs(t, runs)
	var saved loopView
	hub.mustJSON("PATCH", "/api/loops/solo", map[string]string{"subagents": "allowed"}, &saved)
	if saved.Subagents != "allowed" {
		t.Fatalf("after the PATCH subagents = %q, want allowed", saved.Subagents)
	}
	hub.message("solo", "bring help now")
	hub.waitTurn("solo", 30*time.Second, func(done turn) bool { return len(turnArgs(t, runs)) > len(before) })
	for path, line := range turnArgs(t, runs) {
		if _, seen := before[path]; !seen && strings.Contains(line, "--disallowedTools") {
			t.Errorf("the wake after allowing subagents still denied a tool: %s", line)
		}
	}
}

// A cloud loop's session is started with no tool list the hub can set, so
// turning its subagents off is refused rather than stored and not kept
// (#855).
func TestACloudLoopRefusesSubagentsOff(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	refusedSaying(t, hub, "PATCH", "/api/loops/cirrus", map[string]string{"subagents": "off"}, "on a cloud loop")
	refusedSaying(t, hub, "POST", "/api/loops", map[string]any{"name": "stratus", "mission": "m", "runtime": "cloud", "subagents": "off"}, "on a cloud loop")
	var saved loopView
	hub.mustJSON("PATCH", "/api/loops/cirrus", map[string]string{"subagents": "allowed"}, &saved)
	if saved.Subagents != "allowed" {
		t.Fatalf("cirrus subagents = %q, want allowed", saved.Subagents)
	}
}

// refusedSaying wants a 400 whose body says want.
func refusedSaying(t *testing.T, hub *server, method, path string, body any, want string) {
	t.Helper()
	resp, got := hub.do(method, path, body)
	if resp.StatusCode != 400 || !strings.Contains(string(got), want) {
		t.Errorf("%s %s = %d %s, want 400 saying %q", method, path, resp.StatusCode, got, want)
	}
}
