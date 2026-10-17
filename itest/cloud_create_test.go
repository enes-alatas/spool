//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// cloudLoopJSON is what a cloud loop's view carries for the control room.
type cloudLoopJSON struct {
	Runtime         string `json:"runtime"`
	CloudRepo       string `json:"cloud_repo"`
	CloudSessionURL string `json:"cloud_session_url"`
}

// A cloud loop is created as one once Settings → Cloud is green, with the
// GitHub repo its sessions start from (ADR-0056). Its view carries its
// runtime, the repo and, once its first session exists, the session's
// page on claude.ai. Asked of a hub whose account is gone, the creation
// is refused and names the step; a repo on another runtime's loop, a
// docker loop's settings on a cloud loop, a repo that isn't GitHub's, and
// moving a cloud loop are refused too.
func TestACloudLoopIsCreatedWithItsRepo(t *testing.T) {
	t.Parallel()
	s, fake := startCloudHub(t, "cirrus")

	var created cloudLoopJSON
	s.mustJSON("POST", "/api/loops", map[string]any{
		"name": "nimbus", "mission": "integration test loop", "runtime": "cloud",
		"cloud_repo": "https://github.com/example-org/nimbus.git", "idle_timeout_sec": 2,
	}, &created)
	if created.Runtime != "cloud" || created.CloudRepo != "https://github.com/example-org/nimbus" || created.CloudSessionURL != "" {
		t.Fatalf("the created loop: %+v, want a cloud loop with its repo and no session yet", created)
	}

	s.message("nimbus", "first words")
	s.waitTurn("nimbus", 20*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	session := fake.Newest()
	fake.Mu.Lock()
	sources, _ := session.Context["sources"].([]any)
	fake.Mu.Unlock()
	if len(sources) != 1 {
		t.Fatalf("the session's sources: %v, want the repo", sources)
	}
	if source, _ := sources[0].(map[string]any); source["type"] != "git_repository" || source["url"] != "https://github.com/example-org/nimbus" {
		t.Errorf("the session's source: %v, want the repo as a git_repository", sources[0])
	}
	var viewed cloudLoopJSON
	s.mustJSON("GET", "/api/loops/nimbus", nil, &viewed)
	if viewed.CloudSessionURL != "https://claude.ai/code/"+session.ID {
		t.Errorf("the loop's session link = %q, want the session's page", viewed.CloudSessionURL)
	}

	s.wantRefusal("POST", "/api/loops", map[string]any{"name": "stratus", "mission": "m", "runtime": "bare", "cloud_repo": "https://github.com/example-org/stratus"}, 400, "")
	s.wantRefusal("POST", "/api/loops", map[string]any{"name": "stratus", "mission": "m", "runtime": "cloud", "mem_mb": 1024}, 400, "")
	for _, repo := range []string{"http://github.com/example-org/stratus", "https://gitlab.com/example-org/stratus", "https://github.com/example-org/stratus/tree/main", "https://github.com/example-org/.."} {
		s.wantRefusal("POST", "/api/loops", map[string]any{"name": "stratus", "mission": "m", "runtime": "cloud", "cloud_repo": repo}, 400, "")
	}
	s.wantRefusal("POST", "/api/loops/nimbus/rehome", nil, 409, "")

	s.mustJSON("DELETE", "/api/settings/cloud/account", nil, nil)
	resp, body := s.do("POST", "/api/loops", map[string]any{"name": "stratus", "mission": "m", "runtime": "cloud"})
	if resp.StatusCode != 409 || errorCode(body) != "cloud_not_ready" || !strings.Contains(string(body), "sign in") {
		t.Errorf("a cloud loop with no account: %d %s, want 409 cloud_not_ready naming the sign-in", resp.StatusCode, body)
	}
}

// A hub with no public address set refuses a cloud loop and says which
// step to finish.
func TestACloudLoopWaitsForThePublicAddress(t *testing.T) {
	t.Parallel()
	s, _ := startCloudHubAt(t, "cirrus", "")
	resp, body := s.do("POST", "/api/loops", map[string]any{"name": "nimbus", "mission": "m", "runtime": "cloud"})
	if resp.StatusCode != 409 || errorCode(body) != "cloud_not_ready" || !strings.Contains(string(body), "public address") {
		t.Errorf("a cloud loop with no public address: %d %s, want 409 cloud_not_ready naming the address", resp.StatusCode, body)
	}
}
