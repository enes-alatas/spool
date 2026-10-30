//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The first-run read tells a failed first wake from one that has not
// started (#1198). With no wake under way and no turn ever finished clean,
// the loops pillar reads wake_failed, names the loop, and says what failed:
// its workstation did not come up, its claude process crashed, or its turn
// ended in an error. A clean turn after it reads done.
func TestOnboardingNamesAFailedFirstWake(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		script  string
		marker  string // a fakeclaude state file that fails the wake
		failure string
	}{
		{name: "workstation", script: "!echo\n", marker: "mcp-failed", failure: "workstation"},
		{name: "crashed", script: "!crash\n", failure: "crashed"},
		{name: "first_turn", script: "!error\n", failure: "first_turn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workspace := workspaceWithScript(t, tc.script)
			hub := startServer(t, t.TempDir())
			if tc.marker != "" {
				if err := os.MkdirAll(hub.fkState, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(hub.fkState, tc.marker), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			hub.createLoop("faltering", map[string]any{
				"workspace_path": workspace,
				"workspace_mode": "dir",
			})

			view := hub.waitOnboarding(firstTurnWait, func(read onboardingJSON) bool {
				return read.Loops.Progress == "wake_failed"
			})
			if view.Loops.Done || view.Loops.ProgressLoop != "faltering" || view.Loops.Failure != tc.failure {
				t.Fatalf("loops after a failed first wake = %+v, want wake_failed naming faltering, failure %s",
					view.Loops, tc.failure)
			}
			if tc.marker != "" {
				// the code says where to look; the loop's down_reason says why
				if reason := hub.loop("faltering").DownReason; reason != "hub_unreachable" {
					t.Errorf("down_reason = %q, want hub_unreachable for the page to name", reason)
				}
			}
			if view.Loops.Reason != "no loop has woken" {
				t.Errorf("reason = %q, want today's, for a page that does not know the code", view.Loops.Reason)
			}

			if tc.marker != "" {
				if err := os.Remove(filepath.Join(hub.fkState, tc.marker)); err != nil {
					t.Fatal(err)
				}
			}
			// a process reads its script when it starts, so the clean turn
			// waits for the failed one's process to be gone
			if tc.marker == "" {
				hub.waitState("faltering", "asleep", 30*time.Second)
			}
			setScript(t, workspace, "!echo\n")
			hub.message("faltering", "try again")
			view = hub.waitOnboarding(2*time.Minute, func(read onboardingJSON) bool { return read.Loops.Done })
			if view.Loops.Progress != "" || view.Loops.ProgressLoop != "" || view.Loops.Failure != "" {
				t.Errorf("loops once done = %+v, want no progress and no failure", view.Loops)
			}
		})
	}
}
