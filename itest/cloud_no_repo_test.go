//go:build integration

package itest

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cloud loop may name no repo (#1712): it is created with its
// environment alone, its setup check and its sessions start from an empty
// directory that is no git repo, with no --ref, so the session starts
// empty, and it is denied the repo tools. Its prompt says it has no
// checkout, and its sessions are titled after the conversation. A second
// wake sends into the same session. Further repos with no first, or a
// branch with no repo, are refused, and a repo taken away takes its
// branch with it.
func TestACloudLoopWithNoRepoStartsEmpty(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")

	var created cloudLoopJSON
	hub.mustJSON("POST", "/api/loops", map[string]any{
		"name": "nimbus", "mission": "integration test loop", "runtime": "cloud",
		"idle_timeout_sec": 2, "cloud_environment_id": cloudFixtureEnvironment,
	}, &created)
	if created.Runtime != "cloud" || created.CloudRepo != "" || created.CloudBranch != "" || created.CloudEnvironmentID != cloudFixtureEnvironment {
		t.Fatalf("the created loop: %+v, want a cloud loop in its environment with no repo or branch", created)
	}
	hub.message("nimbus", "first words")
	resp, minted := hub.do("POST", "/api/loops/nimbus/cloud/token", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("minting the loop's token: %d", resp.StatusCode)
	}
	setUpCloudEnvironment(t, hub, cloudFixtureEnvironment, string(minted))
	hub.mustJSON("POST", "/api/loops/nimbus/resume", nil, nil)
	passCloudCheck(t, hub, "nimbus")
	first := hub.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	if first.IsError {
		t.Fatalf("the first turn: %+v", first)
	}

	starts := append(checkVMs(t, hub), cloudVMs(t, hub)...)
	if len(starts) < 2 {
		t.Fatalf("the check and the first wake started %d sessions, want 2 or more", len(starts))
	}
	for _, id := range starts {
		vm := filepath.Join(hub.fkState, "cloud", id)
		raw, err := os.ReadFile(filepath.Join(vm, "cli-args"))
		if err != nil {
			t.Fatal(err)
		}
		args := string(raw)
		if !strings.Contains(args, "nimbus") {
			continue // cirrus's own check
		}
		if strings.Contains(args, "\n--ref\n") || strings.Contains(args, "example-org") {
			t.Errorf("a start with no repo ran with\n%s\nwant no --ref and no repo", args)
		}
		if !strings.Contains(args, `"mcp__claude-code-remote__add_repo"`) || !strings.Contains(args, `"mcp__claude-code-remote__list_repos"`) {
			t.Errorf("a start with no repo ran with\n%s\nwant add_repo and list_repos denied", args)
		}
		if entries, err := os.ReadDir(filepath.Join(vm, "repo")); err != nil || len(entries) != 0 {
			t.Errorf("session %s's working directory holds %d entries (%v), want it empty", id, len(entries), err)
		}
	}
	vms := cloudVMs(t, hub)
	var loopVM string
	for _, id := range vms {
		if args, _ := os.ReadFile(filepath.Join(hub.fkState, "cloud", id, "cli-args")); strings.Contains(string(args), "\nnimbus · conversation\n") {
			loopVM = id
		}
	}
	if loopVM == "" {
		t.Fatalf("no session of the loop's was titled \"nimbus · conversation\" among %v", vms)
	}
	context, err := os.ReadFile(filepath.Join(hub.fkState, "cloud", loopVM, "context"))
	if err != nil || !strings.Contains(string(context), "starts empty: you have no checkout and no GitHub repository you can reach.") {
		t.Errorf("the session's context is %q (%v), want the prompt saying it has no repo", context, err)
	}

	hub.waitState("nimbus", "asleep", 20*time.Second)
	hub.message("nimbus", "second words")
	second := hub.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "second words") })
	if second.IsError || second.SessionID != first.SessionID {
		t.Fatalf("the second wake's turn: %+v, want one in session %s", second, first.SessionID)
	}

	for _, refused := range []map[string]any{
		{"cloud_repos": []string{"example-org/tools"}},
		{"cloud_branch": "main"},
	} {
		refused["name"], refused["mission"], refused["runtime"], refused["cloud_environment_id"] = "stratus", "m", "cloud", cloudFixtureEnvironment
		hub.wantRefusal("POST", "/api/loops", refused, 400, "")
	}
	var viewed cloudLoopJSON
	hub.mustJSON("POST", "/api/loops/cirrus/pause", nil, nil)
	hub.mustJSON("PATCH", "/api/loops/cirrus", map[string]any{"cloud_repo": ""}, &viewed)
	if viewed.CloudRepo != "" || viewed.CloudBranch != "" || viewed.CloudEnvironmentID != cloudEnvironmentOf("cirrus") {
		t.Errorf("taking cirrus's repo away: %+v, want no repo, no branch and its environment kept", viewed)
	}
}
