//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// cloudLoopJSON is what a cloud loop's view carries for the control room.
type cloudLoopJSON struct {
	Runtime         string  `json:"runtime"`
	CloudRepo       *string `json:"cloud_repo"`
	CloudSessionURL string  `json:"cloud_session_url"`
}

// A cloud loop is created as one once Settings → Cloud is green. Its view
// carries its runtime and, once its first session exists, the session's
// page on claude.ai. Asked of a hub whose account is gone, the creation is
// refused and names the step; a docker loop's settings on a cloud loop
// and moving a cloud loop are refused too. The repo field went with the
// sessions client (ADR-0059): a creation that still sends one is
// refused, and says what took its place.
func TestACloudLoopIsCreated(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "cirrus")

	var created cloudLoopJSON
	s.mustJSON("POST", "/api/loops", map[string]any{
		"name": "nimbus", "mission": "integration test loop", "runtime": "cloud",
		"idle_timeout_sec": 2,
	}, &created)
	if created.Runtime != "cloud" || created.CloudRepo != nil || created.CloudSessionURL != "" {
		t.Fatalf("the created loop: %+v, want a cloud loop with no repo field and no session yet", created)
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
	for _, runtime := range []string{"cloud", "bare"} {
		resp, body := s.do("POST", "/api/loops", map[string]any{"name": "stratus", "mission": "m", "runtime": runtime, "cloud_repo": "https://github.com/example-org/stratus"})
		if resp.StatusCode != 400 || !strings.Contains(string(body), "GH_TOKEN") {
			t.Errorf("a %s loop with a repo: %d %s, want 400 naming the GH_TOKEN connection", runtime, resp.StatusCode, body)
		}
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
