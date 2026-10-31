//go:build integration

package itest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// adaCloudToken is the access token fakeclaude's sign-in gives the login
// signed in as ada: the fixture account's, with -ada on it.
const adaCloudToken = cloudFixtureToken + "-ada"

// loginEnvironmentsAPI stands in for Anthropic's API, listing each
// account's own environments on its own token: the default login's
// account has the harness's environment for name, and ada's account one
// of its own, adaEnvironment, while listed holds.
func loginEnvironmentsAPI(t *testing.T, name, adaEnvironment string, listed *atomic.Bool) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var environment string
		switch r.Header.Get("Authorization") {
		case "Bearer " + cloudFixtureToken:
			environment = cloudEnvironmentOf(name)
		case "Bearer " + adaCloudToken:
			if listed.Load() {
				environment = adaEnvironment
			}
		default:
			http.Error(w, `{"error":{"type":"authentication_error"}}`, http.StatusUnauthorized)
			return
		}
		body := `{"environments":[],"has_more":false}`
		if environment != "" {
			body = `{"environments":[{"kind":"anthropic_cloud","environment_id":"` + environment + `","name":"spool","state":"active"}],"has_more":false}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(api.Close)
	return api
}

// A cloud loop runs on a Claude login other than the default, in an
// environment of that login's account (ADR-0061, #1078). The move waits
// for the loop to stop, refuses an environment the new login's account
// doesn't list, revokes the hub token the old account's environment
// holds, and makes the setup check stand again. The sessions then
// start, and take their turns, on that login's token, and a check whose
// environment the login no longer lists fails before any start.
func TestACloudLoopRunsOnItsOwnLoginsAccount(t *testing.T) {
	t.Parallel()
	const adaEnvironment = "env_01Adacirrus"
	var listed atomic.Bool
	listed.Store(true)
	hub := startCloudHub(t, "cirrus", "--cloud-api-base", loginEnvironmentsAPI(t, "cirrus", adaEnvironment, &listed).URL)
	hub.mustJSON("POST", "/api/claude-logins", map[string]string{"name": "ada"}, nil)
	hub.signInClaudeLogin("ada", "ada")

	var environments struct {
		Environments []struct{ ID string } `json:"environments"`
	}
	hub.mustJSON("GET", "/api/settings/cloud/environments?login=ada", nil, &environments)
	if len(environments.Environments) != 1 || environments.Environments[0].ID != adaEnvironment {
		t.Fatalf("ada's environments = %+v, want her account's own", environments.Environments)
	}
	hub.wantRefusal("GET", "/api/settings/cloud/environments?login=nobody", nil, http.StatusNotFound, "claude_login")

	hub.wantRefusal("PATCH", "/api/loops/cirrus", map[string]any{"claude_login": "ada"}, http.StatusConflict, "cloud_loop_running")
	hub.mustJSON("POST", "/api/loops/cirrus/pause", nil, nil)
	hub.wantRefusal("PATCH", "/api/loops/cirrus", map[string]any{"claude_login": "ada"}, http.StatusBadRequest, "cloud_environment_unlisted")
	hub.mustJSON("PATCH", "/api/loops/cirrus", map[string]any{"claude_login": "ada", "cloud_environment_id": adaEnvironment}, nil)

	// the old account's environment keeps the token as its secret, so the
	// move revokes it, and the loop waits for a token of its own
	old := cloudTokenOf("cirrus")
	hub.mustJSON("POST", "/api/loops/cirrus/resume", nil, nil)
	if got := postHook(t, hub, old, "session_01Probe"); got != http.StatusUnauthorized {
		t.Errorf("the token the old account's environment holds, after the move: %d, want 401", got)
	}
	if held := hub.waitHealth("cirrus", "failing"); len(held.Problems) != 1 || held.Problems[0].Symptom != store.SymptomCloudSetup {
		t.Fatalf("problems after the move = %+v, want one cloud_setup", held.Problems)
	}
	resp, body := hub.do("POST", "/api/loops/cirrus/cloud/token", nil)
	if resp.StatusCode != http.StatusOK || len(body) == 0 || string(body) == old {
		t.Fatalf("Rotate token after the move answered %d, want a new token", resp.StatusCode)
	}
	setUpCloudEnvironment(t, hub, adaEnvironment, string(body))
	hub.mustJSON("POST", "/api/loops/cirrus/resume", nil, nil)
	var setup cloudLoopSetup
	hub.mustJSON("GET", "/api/loops/cirrus/cloud/setup", nil, &setup)
	if setup.Check == nil || setup.Check.Current {
		t.Fatalf("the check after the move = %+v, want it no longer current", setup.Check)
	}
	passCloudCheck(t, hub, "cirrus")

	hub.message("cirrus", "words on ada")
	turn := hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "words on ada") })
	if turn.IsError {
		t.Fatalf("the turn on ada: %+v", turn)
	}
	vms := cloudVMs(t, hub)
	if len(vms) == 0 {
		t.Fatal("no session started")
	}
	sum := sha256.Sum256([]byte(adaCloudToken))
	want := hex.EncodeToString(sum[:])
	raw, err := os.ReadFile(filepath.Join(hub.fkState, "cloud", vms[len(vms)-1], "account-sums"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Fields(string(raw)) {
		if line != want {
			t.Errorf("a CLI run of ada's session carried another account's token (sha256 %s…)", line[:8])
		}
	}
	if environment, _ := os.ReadFile(filepath.Join(hub.fkState, "cloud", vms[len(vms)-1], "environment")); string(environment) != adaEnvironment {
		t.Errorf("ada's session started in %q, want %s", environment, adaEnvironment)
	}

	// ada's account no longer lists the environment: the check is held
	// before it starts anything
	listed.Store(false)
	before := len(startedVMs(t, hub))
	view := checkCloudLoop(t, hub, "cirrus")
	if view.Check.State != "failed" || view.Check.Stage != "environment" {
		t.Errorf("the check with ada's list empty = %+v, want failed at environment", view.Check)
	}
	if after := len(startedVMs(t, hub)); after != before {
		t.Errorf("the held check started %d sessions, want none", after-before)
	}
}
