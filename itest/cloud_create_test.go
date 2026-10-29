//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// cloudLoopJSON is what a cloud loop's view carries for the control room.
type cloudLoopJSON struct {
	Runtime            string   `json:"runtime"`
	CloudRepo          string   `json:"cloud_repo"`
	CloudBranch        string   `json:"cloud_branch"`
	CloudRepos         []string `json:"cloud_repos"`
	CloudEnvironmentID string   `json:"cloud_environment_id"`
	CloudSessionURL    string   `json:"cloud_session_url"`
}

// A cloud loop is created as one once Settings → Cloud is green. Its view
// carries its runtime and, once its first session exists, the session's
// page on claude.ai. Asked of a hub whose account is gone, the creation is
// refused and names the step; a docker loop's settings on a cloud loop
// and moving a cloud loop are refused too. A cloud loop names the repos and
// the environment its sessions start from (ADR-0061): checked when they
// are set, refused on any other runtime, and changed only while the loop
// is paused.
func TestACloudLoopIsCreated(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "cirrus")

	var created cloudLoopJSON
	s.mustJSON("POST", "/api/loops", map[string]any{
		"name": "nimbus", "mission": "integration test loop", "runtime": "cloud",
		"idle_timeout_sec": 2, "cloud_repo": " example-org/nimbus ", "cloud_repos": []string{"example-org/tools"},
		"cloud_environment_id": cloudFixtureEnvironment,
	}, &created)
	if created.Runtime != "cloud" || created.CloudRepo != "example-org/nimbus" || created.CloudBranch != "main" ||
		len(created.CloudRepos) != 1 || created.CloudEnvironmentID != cloudFixtureEnvironment || created.CloudSessionURL != "" {
		t.Fatalf("the created loop: %+v, want a cloud loop on its repo's main, in its environment, with no session yet", created)
	}

	s.message("nimbus", "first words")
	s.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	vms := cloudVMs(t, s)
	if len(vms) != 1 {
		t.Fatalf("the first turn started %d sessions, want 1", len(vms))
	}
	var viewed cloudLoopJSON
	s.mustJSON("GET", "/api/loops/nimbus", nil, &viewed)
	if viewed.CloudSessionURL != "https://claude.ai/code/"+vms[0] {
		t.Errorf("the loop's session link = %q, want the session's page", viewed.CloudSessionURL)
	}

	s.wantRefusal("POST", "/api/loops", map[string]any{"name": "stratus", "mission": "m", "runtime": "cloud", "mem_mb": 1024}, 400, "")
	for _, refused := range []map[string]any{
		{"runtime": "bare", "cloud_repo": "example-org/stratus"},
		{"runtime": "cloud", "cloud_repo": "https://github.com/example-org/stratus", "cloud_environment_id": cloudFixtureEnvironment},
		{"runtime": "cloud", "cloud_repo": "example-org/stratus", "cloud_branch": "-x", "cloud_environment_id": cloudFixtureEnvironment},
		{"runtime": "cloud", "cloud_repo": "example-org/stratus", "cloud_environment_id": "ccpool_01Laptop"},
		{"runtime": "cloud", "cloud_repo": "example-org/stratus"},
	} {
		refused["name"], refused["mission"] = "stratus", "m"
		s.wantRefusal("POST", "/api/loops", refused, 400, "")
	}

	s.wantRefusal("PATCH", "/api/loops/nimbus", map[string]any{"cloud_branch": "dev"}, 409, "cloud_loop_running")
	s.mustJSON("POST", "/api/loops/nimbus/pause", nil, nil)
	s.mustJSON("PATCH", "/api/loops/nimbus", map[string]any{"cloud_branch": "dev", "cloud_repos": []string{}}, &viewed)
	if viewed.CloudBranch != "dev" || len(viewed.CloudRepos) != 0 || viewed.CloudRepo != "example-org/nimbus" {
		t.Errorf("the paused loop's edit: %+v, want its branch dev, no further repos and its repo kept", viewed)
	}
	s.wantRefusal("POST", "/api/loops/nimbus/rehome", nil, 409, "")

	s.signOutCloudAccount()
	resp, body := s.do("POST", "/api/loops", map[string]any{"name": "stratus", "mission": "m", "runtime": "cloud"})
	if resp.StatusCode != 409 || errorCode(body) != "cloud_not_ready" || !strings.Contains(string(body), "sign in") {
		t.Errorf("a cloud loop with no account: %d %s, want 409 cloud_not_ready naming the sign-in", resp.StatusCode, body)
	}
}

// A hub with no public address set refuses a cloud loop and says which
// step to finish.
func TestACloudLoopWaitsForThePublicAddress(t *testing.T) {
	t.Parallel()
	s := startCloudHubAt(t, "cirrus", "")
	resp, body := s.do("POST", "/api/loops", map[string]any{"name": "nimbus", "mission": "m", "runtime": "cloud"})
	if resp.StatusCode != 409 || errorCode(body) != "cloud_not_ready" || !strings.Contains(string(body), "public address") {
		t.Errorf("a cloud loop with no public address: %d %s, want 409 cloud_not_ready naming the address", resp.StatusCode, body)
	}
}
