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
// environments on the fixture account's token only: the one the page
// picks, and cirrus's own, which its starts confirm (#1232).
func environmentsAPI(t *testing.T) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/environment_providers" || r.Header.Get("Authorization") != "Bearer "+cloudFixtureToken {
			http.Error(w, `{"error":{"type":"not_found_error"}}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"environments":[
			{"kind":"anthropic_cloud","environment_id":"env_01Picked","name":"spool","state":"active"},
			{"kind":"anthropic_cloud","environment_id":"` + cloudEnvironmentOf("cirrus") + `","name":"cirrus","state":"active"},
			{"kind":"bridge","environment_id":"env_01Laptop","name":"laptop","state":"active"}
		],"has_more":false}`))
	}))
	t.Cleanup(api.Close)
	return api
}

// Settings → Cloud's environment step still lists, picks and confirms, and
// its confirm stands as before (ADR-0059), until it goes with the control
// room's step (#1183). A loop no longer starts in the picked environment
// but in its own, and carries the warning by its own confirm (ADR-0061).
func TestACloudLoopStartsInItsOwnEnvironmentNotThePagesPick(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus", "--cloud-api-base", environmentsAPI(t).URL)

	var listed struct {
		Environments []struct{ ID, Name string } `json:"environments"`
	}
	hub.mustJSON("GET", "/api/settings/cloud/environments", nil, &listed)
	if len(listed.Environments) != 2 || listed.Environments[0].ID != "env_01Picked" || listed.Environments[0].Name != "spool" {
		t.Fatalf("the list = %+v, want the two cloud environments", listed.Environments)
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
	// the page's confirm no longer speaks for any loop: each loop confirms
	// its own environment's list on its setup view (ADR-0061)
	hub.mustJSON("POST", "/api/settings/cloud/environment/confirm", map[string]string{"digest": step.Environment.Digest}, &step)
	if !step.Environment.Confirmed || !cloudLoopWarned(t, hub, "cirrus") {
		t.Errorf("after the page's confirm: %+v, warned %v, want the loop still warned", step.Environment, cloudLoopWarned(t, hub, "cirrus"))
	}
	var setup cloudLoopSetup
	hub.mustJSON("GET", "/api/loops/cirrus/cloud/setup", nil, &setup)
	hub.mustJSON("POST", "/api/loops/cirrus/cloud/allowlist/confirm", map[string]string{"digest": setup.Allowlist.Digest}, &setup)
	if !setup.Allowlist.Confirmed || cloudLoopWarned(t, hub, "cirrus") {
		t.Errorf("after the loop's confirm: %+v, warned %v", setup.Allowlist, cloudLoopWarned(t, hub, "cirrus"))
	}

	hub.message("cirrus", "hello")
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "hello") })
	vms := cloudVMs(t, hub)
	if len(vms) != 1 {
		t.Fatalf("one start made %d sessions", len(vms))
	}
	if environment, err := os.ReadFile(filepath.Join(hub.fkState, "cloud", vms[0], "environment")); err != nil || string(environment) != cloudEnvironmentOf("cirrus") {
		t.Errorf("the session started in %q (%v), want the loop's own", environment, err)
	}

	hub.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": "tools.example.test"}, nil)
	if !cloudLoopWarned(t, hub, "cirrus") {
		t.Error("an allowlist edit left the loop without the warning")
	}
	hub.wantRefusal("POST", "/api/settings/cloud/environment/confirm", map[string]string{"digest": step.Environment.Digest}, 409, "cloud_allowlist_stale")
	hub.wantRefusal("POST", "/api/loops/cirrus/cloud/allowlist/confirm", map[string]string{"digest": setup.Allowlist.Digest}, 409, "cloud_allowlist_stale")
}

// A cloud loop is created with no repo or environment, and is held until
// its setup is done: a start without an environment would run in whichever
// one the CLI picked (ADR-0061). The account's list fails here, so the ID
// is pasted, while the loop is stopped. A pasted ID isn't started in until
// the list shows it, on the default login too (#1232): the check fails at
// the environment and starts nothing while the list is down.
func TestCloudLoopsWaitForTheirEnvironment(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	down := filepath.Join(hub.fkState, "environments-down")
	if err := os.WriteFile(down, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hub.mustJSON("POST", "/api/loops", map[string]any{"name": "nimbus", "mission": "m", "runtime": "cloud"}, nil)
	if held := hub.waitHealth("nimbus", "failing"); len(held.Problems) != 1 || held.Problems[0].Symptom != "cloud_setup" {
		t.Fatalf("problems of a loop with no environment = %+v, want one cloud_setup", held.Problems)
	}
	resp, minted := hub.do("POST", "/api/loops/nimbus/cloud/token", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("minting the loop's token: %d", resp.StatusCode)
	}
	makeGitHubRepo(t, hub.fkState, "example-org/nimbus")
	setUpCloudEnvironment(t, hub, "env_01Pasted", string(minted))
	hub.message("nimbus", "held words")
	hub.mustJSON("POST", "/api/loops/nimbus/resume", nil, nil)
	time.Sleep(3 * time.Second)
	if vms := cloudVMs(t, hub); len(vms) != 0 {
		t.Fatalf("a loop with no environment started %d sessions", len(vms))
	}

	hub.mustJSON("POST", "/api/loops/nimbus/pause", nil, nil)
	hub.wantRefusal("PATCH", "/api/loops/nimbus", map[string]any{"cloud_repo": "example-org/nimbus", "cloud_environment_id": `env_01","x":"y`}, 400, "")
	hub.mustJSON("PATCH", "/api/loops/nimbus", map[string]any{"cloud_repo": "example-org/nimbus", "cloud_environment_id": "env_01Pasted"}, nil)
	hub.mustJSON("POST", "/api/loops/nimbus/resume", nil, nil)
	before := len(startedVMs(t, hub))
	if view := checkCloudLoop(t, hub, "nimbus"); view.Check.State != "failed" || view.Check.Stage != "environment" || !strings.Contains(view.Check.Detail, "can't be listed") {
		t.Errorf("the check while the list is down = %+v, want failed at environment", view.Check)
	}
	if after := len(startedVMs(t, hub)); after != before {
		t.Errorf("the held check started %d sessions, want none", after-before)
	}
	if err := os.Remove(down); err != nil {
		t.Fatal(err)
	}
	passCloudCheck(t, hub, "nimbus")
	hub.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })
	if after := hub.waitHealth("nimbus", "ok"); len(after.Problems) != 0 {
		t.Errorf("problems once its setup is done = %+v, want none", after.Problems)
	}
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
