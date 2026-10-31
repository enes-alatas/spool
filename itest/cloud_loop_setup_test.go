//go:build integration

package itest

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/runtime/cloud"
	"github.com/enes-alatas/spool/internal/store"
)

// A cloud loop's setup view shows what to paste into its environment: the
// hub's setup script, the host its token is the network secret for, and
// the allowed hosts to confirm. It says which steps are left, and never
// carries the token, which the mint shows once. The check is the last
// step: it starts one session in the environment, and a pass lets the
// held loop wake with what waited. A Rotate token makes it stale
// (ADR-0061, #1182).
func TestACloudLoopsSetupViewWalksItsStepsAndNeverShowsItsToken(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	hub.mustJSON("POST", "/api/loops", map[string]any{
		"name": "nimbus", "mission": "m", "runtime": "cloud",
		"cloud_repo": "example-org/nimbus", "cloud_environment_id": cloudFixtureEnvironment,
	}, nil)

	var view cloudLoopSetup
	hub.mustJSON("GET", "/api/loops/nimbus/cloud/setup", nil, &view)
	script, err := cloud.SetupScript(hub.cloudURL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if view.Script != script {
		t.Errorf("the view's script isn't the hub's setup script for its public address:\n%s", view.Script)
	}
	if want := strings.TrimPrefix(hub.cloudURL, "http://"); view.SecretHost != want {
		t.Errorf("secret_host = %q, want the public address's host %q", view.SecretHost, want)
	}
	if want := []string{"token", "allowlist", "check"}; !slices.Equal(view.Steps, want) || view.Token.Set || view.Check != nil {
		t.Fatalf("a new loop's view: steps %v, token %v, check %+v; want %v, no token, no check", view.Steps, view.Token.Set, view.Check, want)
	}

	resp, minted := hub.do("POST", "/api/loops/nimbus/cloud/token", nil)
	token := string(minted)
	if resp.StatusCode != http.StatusOK || token == "" {
		t.Fatalf("minting the loop's token: %d", resp.StatusCode)
	}
	for _, path := range []string{"/api/loops/nimbus/cloud/setup", "/api/loops/nimbus", "/api/loops"} {
		if _, body := hub.do("GET", path, nil); strings.Contains(string(body), token) {
			t.Errorf("GET %s carries the loop's token", path)
		}
	}
	hub.mustJSON("GET", "/api/loops/nimbus/cloud/setup", nil, &view)
	if !view.Token.Set || slices.Contains(view.Steps, "token") {
		t.Errorf("after the mint: token %v, steps %v, want it set and the step done", view.Token.Set, view.Steps)
	}

	hub.wantRefusal("POST", "/api/loops/nimbus/cloud/allowlist/confirm", map[string]string{"digest": "stale"}, 409, "cloud_allowlist_stale")
	hub.mustJSON("POST", "/api/loops/nimbus/cloud/allowlist/confirm", map[string]string{"digest": view.Allowlist.Digest}, &view)
	if !view.Allowlist.Confirmed || slices.Contains(view.Steps, "allowlist") {
		t.Errorf("after the confirm: %+v, steps %v", view.Allowlist, view.Steps)
	}

	// the mint stopped the loop, whose token the hub refuses until it starts
	hub.wantRefusal("POST", "/api/loops/nimbus/cloud/check", nil, 409, "cloud_check_refused")
	makeGitHubRepo(t, hub.fkState, "example-org/nimbus")
	setUpCloudEnvironment(t, hub, cloudFixtureEnvironment, token)
	hub.message("nimbus", "held words")
	hub.mustJSON("POST", "/api/loops/nimbus/resume", nil, nil)
	time.Sleep(time.Second)
	if vms := cloudVMs(t, hub); len(vms) != 0 {
		t.Fatalf("an unchecked loop started %d sessions, want none", len(vms))
	}
	if held := hub.waitHealth("nimbus", "failing"); len(held.Problems) != 1 || held.Problems[0].Symptom != store.SymptomCloudSetup {
		t.Fatalf("problems of an unchecked loop = %+v, want one cloud_setup", held.Problems)
	}

	view = checkCloudLoop(t, hub, "nimbus")
	if view.Check.State != "ok" || !view.Check.Current || len(view.Steps) != 0 {
		t.Fatalf("after a check that reached the hub: %+v, steps %v; want a current pass and nothing left", view.Check, view.Steps)
	}
	if checks := checkVMs(t, hub); len(checks) != 2 {
		t.Errorf("%d setup check sessions, want cirrus's and nimbus's", len(checks))
	}
	hub.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })

	if resp, _ := hub.do("POST", "/api/loops/nimbus/cloud/token", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("Rotate token: %d", resp.StatusCode)
	}
	hub.mustJSON("GET", "/api/loops/nimbus/cloud/setup", nil, &view)
	if view.Check == nil || view.Check.Current || !slices.Contains(view.Steps, "check") {
		t.Errorf("after Rotate token: check %+v, steps %v; want the pass stale and the check to do again", view.Check, view.Steps)
	}
}

// A check that fails says where it stopped: a secret that isn't the loop's
// token reaches the hub and is refused, a hub the environment's network
// can't reach hears nothing, and an environment the account no longer has
// makes the CLI pick another, which the check catches. Each holds the
// loop until a check passes again.
func TestTheSetupCheckSaysWhereItStopped(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus", "--cloud-start-within-sec", "3")
	environment := filepath.Join(hub.fkState, "environments", cloudEnvironmentOf("cirrus"))
	failsAt := func(stage, says string) {
		t.Helper()
		view := checkCloudLoop(t, hub, "cirrus")
		if view.Check.State != "failed" || view.Check.Stage != stage || !strings.Contains(view.Check.Detail, says) {
			t.Fatalf("the check = %+v, want it failed at %s, saying %q", view.Check, stage, says)
		}
		if held := hub.waitHealth("cirrus", "failing"); len(held.Problems) != 1 || held.Problems[0].Symptom != store.SymptomCloudSetup {
			t.Fatalf("problems after a failed check = %+v, want one cloud_setup", held.Problems)
		}
	}

	setUpCloudEnvironment(t, hub, cloudEnvironmentOf("cirrus"), "spool-itest-synthetic-not-the-token")
	failsAt("token", "network secret")
	setUpCloudEnvironment(t, hub, cloudEnvironmentOf("cirrus"), cloudTokenOf("cirrus"))

	refused := filepath.Join(hub.fkState, "cloud-egress-refused")
	if err := os.WriteFile(refused, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	failsAt("unreached", "allowed hosts")
	if err := os.Remove(refused); err != nil {
		t.Fatal(err)
	}

	// gone between the hub's list and the start: the CLI falls back to
	// another of the account's environments, which the check catches in
	// its debug log (#1232)
	setUpCloudEnvironment(t, hub, "env_01Another", cloudTokenOf("cirrus"))
	gone := filepath.Join(environment, "gone")
	if err := os.WriteFile(gone, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	failsAt("environment", "picked environment env_01Another")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	passCloudCheck(t, hub, "cirrus")
	if after := hub.waitHealth("cirrus", "ok"); len(after.Problems) != 0 {
		t.Errorf("problems once a check passed again = %+v, want none", after.Problems)
	}
}
