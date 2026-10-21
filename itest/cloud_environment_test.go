//go:build integration

package itest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cloudEnvironmentJSON is Settings → Cloud's environment step.
type cloudEnvironmentJSON struct {
	Environment struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		Allowlist  string   `json:"allowlist"`
		NotApplied []string `json:"not_applied"`
		Digest     string   `json:"digest"`
		Confirmed  bool     `json:"confirmed"`
	} `json:"environment"`
}

// environmentsAPI stands in for Anthropic's API, listing the account's
// environments on the fixture account's token only.
func environmentsAPI(t *testing.T) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/environment_providers" || r.Header.Get("Authorization") != "Bearer "+cloudFixtureToken {
			http.Error(w, `{"error":{"type":"not_found_error"}}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"environments":[
			{"kind":"anthropic_cloud","environment_id":"env_01Picked","name":"spool","state":"active"},
			{"kind":"bridge","environment_id":"env_01Laptop","name":"laptop","state":"active"}
		],"has_more":false}`))
	}))
	t.Cleanup(api.Close)
	return api
}

// Settings → Cloud picks the environment by name from the account's list,
// and the loop's next start runs in it (ADR-0059). Until the operator
// confirms pasting the current list, the loop carries the warning that
// the hub's allowlist doesn't bind it; an edit to the allowlist brings the
// warning back.
func TestACloudLoopStartsInTheEnvironmentThePagePicked(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus", "--cloud-api-base", environmentsAPI(t).URL)

	var listed struct {
		Environments []struct{ ID, Name string } `json:"environments"`
	}
	hub.mustJSON("GET", "/api/settings/cloud/environments", nil, &listed)
	if len(listed.Environments) != 1 || listed.Environments[0].ID != "env_01Picked" || listed.Environments[0].Name != "spool" {
		t.Fatalf("the list = %+v, want the one cloud environment", listed.Environments)
	}
	hub.wantRefusal("PUT", "/api/settings/cloud/environment", map[string]string{"id": "env_01Laptop"}, 400, "cloud_environment_unlisted")
	var step cloudEnvironmentJSON
	hub.mustJSON("PUT", "/api/settings/cloud/environment", map[string]string{"id": "env_01Picked"}, &step)
	if step.Environment.Name != "spool" || step.Environment.Confirmed {
		t.Fatalf("picked: %+v, want spool, unconfirmed", step.Environment)
	}
	if !strings.HasPrefix(step.Environment.Allowlist, "127.0.0.1\n") {
		t.Errorf("the list to paste starts %q, want the public address's host", strings.SplitN(step.Environment.Allowlist, "\n", 2)[0])
	}
	if !cloudLoopWarned(t, hub, "cirrus") {
		t.Error("a loop with no confirmed list carries no warning")
	}
	hub.mustJSON("POST", "/api/settings/cloud/environment/confirm", map[string]string{"digest": step.Environment.Digest}, &step)
	if !step.Environment.Confirmed || cloudLoopWarned(t, hub, "cirrus") {
		t.Errorf("after the confirm: %+v, warned %v", step.Environment, cloudLoopWarned(t, hub, "cirrus"))
	}

	hub.message("cirrus", "hello")
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "hello") })
	vms := cloudVMs(t, hub)
	if len(vms) != 1 {
		t.Fatalf("one start made %d sessions", len(vms))
	}
	if environment, err := os.ReadFile(filepath.Join(hub.fkState, "cloud", vms[0], "environment")); err != nil || string(environment) != "env_01Picked" {
		t.Errorf("the session started in %q (%v), want env_01Picked", environment, err)
	}

	hub.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": "tools.example.test"}, nil)
	if !cloudLoopWarned(t, hub, "cirrus") {
		t.Error("an allowlist edit left the loop without the warning")
	}
	hub.wantRefusal("POST", "/api/settings/cloud/environment/confirm", map[string]string{"digest": step.Environment.Digest}, 409, "cloud_allowlist_stale")
}

// With no environment chosen a cloud loop can't be created, and one that
// exists is held, waking once one is chosen: a start without one would
// run in whichever environment the CLI picked. The account's list fails
// here, so the ID is pasted.
func TestCloudLoopsWaitForAnEnvironment(t *testing.T) {
	t.Parallel()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)
	hub := startCloudHub(t, "cirrus", "--cloud-api-base", down.URL)
	hub.mustJSON("DELETE", "/api/settings/cloud/environment", nil, nil)
	resp, body := hub.do("POST", "/api/loops", map[string]any{"name": "nimbus", "mission": "m", "runtime": "cloud"})
	if resp.StatusCode != 409 || errorCode(body) != "cloud_not_ready" || !strings.Contains(string(body), "environment") {
		t.Errorf("a cloud loop with no environment: %d %s, want 409 cloud_not_ready naming the environment", resp.StatusCode, body)
	}
	hub.wantRefusal("PUT", "/api/settings/cloud/environment", map[string]string{"id": `env_01","x":"y`}, 400, "cloud_environment_invalid")

	hub.message("cirrus", "held words")
	time.Sleep(3 * time.Second)
	if vms := cloudVMs(t, hub); len(vms) != 0 {
		t.Fatalf("a loop with no environment started %d sessions", len(vms))
	}
	hub.mustJSON("PUT", "/api/settings/cloud/environment", map[string]string{"id": "env_01Pasted"}, nil)
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })
}

// cloudLoopWarned reports whether the loop's view carries the warning that
// its environment's list isn't confirmed.
func cloudLoopWarned(t *testing.T, hub *server, name string) bool {
	t.Helper()
	var viewed struct {
		Unconfirmed bool `json:"cloud_allowlist_unconfirmed"`
	}
	hub.mustJSON("GET", "/api/loops/"+name, nil, &viewed)
	return viewed.Unconfirmed
}
