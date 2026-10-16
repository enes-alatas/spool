//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/runtime/cloud/cloudfake"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// The address cloud sessions reach the hub at, in these tests: the fake
// API never connects to it, so it only has to be written in the right
// places.
const testCloudAddress = "https://hub.example.test"

// The account token the fake API admits: synthetic, never a real one.
const cloudFixtureToken = "sk-ant-oat01-fixture-cloud-account"

// startCloudHub starts a hub with one cloud loop, its account signed in and
// its public address set, driven through a fake of Anthropic's sessions API.
func startCloudHub(t *testing.T, name string) (*server, *cloudfake.Server) {
	t.Helper()
	return startCloudHubAt(t, name, testCloudAddress)
}

// startCloudHubAt is startCloudHub with the public address given.
func startCloudHubAt(t *testing.T, name, address string) (*server, *cloudfake.Server) {
	t.Helper()
	fake := cloudfake.New(cloudFixtureToken)
	t.Cleanup(fake.Close)
	dataDir := t.TempDir()
	s := startServerArgs(t, dataDir, "--runtime", "bare")
	s.createLoop(name, nil)
	s.stop()
	makeCloudLoop(t, dataDir, name)
	signInCloud(t, dataDir, address)
	return startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-api-base", fake.URL), fake
}

// signInCloud stores the synthetic account and the public address, as
// Settings → Cloud will.
func signInCloud(t *testing.T, dataDir, address string) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	// A cloud loop is created as one, so it has run no session elsewhere:
	// the one its bare start ran is forgotten.
	loops, err := db.Loops().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, loopRecord := range loops {
		if err := db.Loops().SetRuntime(ctx, loopRecord.ID, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	account, _ := json.Marshal(store.CloudAccount{Token: cloudFixtureToken, OrgUUID: "00000000-0000-4000-8000-000000000001"})
	if err := db.Settings().Set(ctx, store.SettingCloudAccount, string(account)); err != nil {
		t.Fatal(err)
	}
	if err := db.Settings().Set(ctx, store.SettingCloudAddress, address); err != nil {
		t.Fatal(err)
	}
}

// A cloud loop's turns run in a cloud session of its own: the session is
// created with the loop's system prompt and its hub token as a session
// variable, in an environment whose setup script registers the hub by the
// public address and names that variable. Its claude's events read back
// as a contained turn's do, cost included, and the next wake, after the
// loop slept, carries on in the same session.
func TestCloudLoopTurnsRunInASessionOfItsOwn(t *testing.T) {
	t.Parallel()
	s, fake := startCloudHub(t, "cirrus")

	s.message("cirrus", "first words")
	first := s.waitTurn("cirrus", 20*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	if first.IsError || first.CostUSD == 0 {
		t.Fatalf("the cloud turn: %+v", first)
	}

	fake.Mu.Lock()
	if len(fake.Environments) != 1 || len(fake.Sessions) != 1 {
		t.Fatalf("one turn made %d environments and %d sessions", len(fake.Environments), len(fake.Sessions))
	}
	var script string
	for _, environment := range fake.Environments {
		script = environment.Body["config"].(map[string]any)["init_script"].(string)
	}
	var sessionContext map[string]any
	for _, session := range fake.Sessions {
		sessionContext = session.Context
	}
	fake.Mu.Unlock()
	if !strings.Contains(script, "'"+testCloudAddress+"/mcp'") || !strings.Contains(script, "${SPOOL_HUB_TOKEN}") {
		t.Errorf("the setup script doesn't register the hub at its public address by the token's variable:\n%s", script)
	}
	env := sessionContext["environment_variables"].(map[string]any)
	if env["SPOOL_HUB_TOKEN"] != hubMCPToken(t, s, "cirrus") || len(env) != 1 {
		t.Errorf("the session's variables are not the loop's hub token alone: %d of them", len(env))
	}
	if prompt, _ := sessionContext["append_system_prompt"].(string); !strings.Contains(prompt, "cirrus") {
		t.Errorf("the session's system prompt is not the loop's: %.200q", prompt)
	}

	s.waitState("cirrus", "asleep", 20*time.Second)
	s.message("cirrus", "second words")
	second := s.waitTurn("cirrus", 20*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "second words") })
	if second.IsError || second.SessionID != first.SessionID {
		t.Fatalf("the second wake's turn: %+v, want one in session %s", second, first.SessionID)
	}
	fake.Mu.Lock()
	sessions := len(fake.Sessions)
	fake.Mu.Unlock()
	if sessions != 1 {
		t.Fatalf("a second wake made %d sessions, want it to carry on in the one", sessions)
	}
}

// A cloud turn whose session goes idle with no result written ends as an
// error that says so, rather than leaving the loop busy for good.
func TestCloudTurnIdleWithNoResultEndsAsAnError(t *testing.T) {
	t.Parallel()
	s, fake := startCloudHub(t, "nimbus")
	fake.Mu.Lock()
	fake.Reply = func(session *cloudfake.Session, _ string) []map[string]any {
		return []map[string]any{cloudfake.Init(session), cloudfake.Text("started, never finished")}
	}
	fake.Mu.Unlock()

	s.message("nimbus", "hello")
	ended := s.waitTurn("nimbus", 60*time.Second, func(turn turn) bool { return turn.Trigger != store.TriggerTick })
	if !ended.IsError || !strings.Contains(ended.ResultText, "session_status idle") {
		t.Fatalf("the turn ended as %+v, want an error naming the idle status", ended)
	}
}

// Deleting a cloud loop archives its session and its environment, so
// nothing starts in either again.
func TestDeletingACloudLoopArchivesItsSessionAndEnvironment(t *testing.T) {
	t.Parallel()
	s, fake := startCloudHub(t, "stratus")
	s.message("stratus", "hello")
	s.waitTurn("stratus", 20*time.Second, func(turn turn) bool { return turn.Trigger != store.TriggerTick })

	s.mustJSON("DELETE", "/api/loops/stratus", nil, nil)
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	for _, environment := range fake.Environments {
		if !environment.Archived {
			t.Error("the deleted loop's environment is not archived")
		}
	}
	for _, session := range fake.Sessions {
		if !session.Archived {
			t.Error("the deleted loop's session is not archived")
		}
	}
}

// A cloud address the loop's token would cross in the clear is refused at
// use: the loop doesn't wake, and nothing reaches the API, whatever wrote
// the address.
func TestCloudLoopDoesNotWakeOnAPlainHTTPAddress(t *testing.T) {
	t.Parallel()
	s, fake := startCloudHubAt(t, "cumulus", "http://hub.example.test")
	s.message("cumulus", "hello")
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(s.log(), "must be https"); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the hub log doesn't say why the loop didn't wake:\n%s", s.log())
		}
	}
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	if len(fake.Environments) != 0 || len(fake.Sessions) != 0 {
		t.Fatalf("a plain-HTTP address made %d environments and %d sessions", len(fake.Environments), len(fake.Sessions))
	}
}

// cloudEgressSettings is GET /api/settings/egress's cloud part.
type cloudEgressSettings struct {
	Cloud *struct {
		Environments []struct {
			Loop   string `json:"loop"`
			InSync bool   `json:"in_sync"`
			Error  string `json:"error"`
		} `json:"environments"`
		NotApplied []string `json:"not_applied"`
	} `json:"cloud"`
}

// allowedHosts reads the one environment's allowed hosts from the fake.
func allowedHosts(t *testing.T, fake *cloudfake.Server) []string {
	t.Helper()
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	if len(fake.Environments) != 1 {
		t.Fatalf("%d environments, want one", len(fake.Environments))
	}
	var hosts []string
	for _, environment := range fake.Environments {
		network := environment.Body["config"].(map[string]any)["network_config"].(map[string]any)
		for _, host := range network["allowed_hosts"].([]any) {
			hosts = append(hosts, host.(string))
		}
	}
	return hosts
}

// An edit to the egress allowlist reaches a cloud loop's environment at
// once, with no wake of the loop, a domain rule as the domain and every
// name under it. Settings reads the environment as in sync, marks the
// entry with a port of its own as not applied to cloud loops, and names a
// write that failed until one lands, to an admin: a member reads only
// that it is out of sync (ADR-0056).
func TestAnEgressEditReachesTheCloudEnvironmentsAtOnce(t *testing.T) {
	t.Parallel()
	fake := cloudfake.New(cloudFixtureToken)
	t.Cleanup(fake.Close)
	dataDir := t.TempDir()
	s := startServerArgs(t, dataDir, "--runtime", "bare", "--egress-allow", "registry.example.test:8443")
	s.createLoop("cirrus", nil)
	s.stop()
	makeCloudLoop(t, dataDir, "cirrus")
	signInCloud(t, dataDir, testCloudAddress)
	s = startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-api-base", fake.URL)

	s.message("cirrus", "make the environment")
	s.waitTurn("cirrus", 20*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "make the environment") })
	s.waitState("cirrus", "asleep", 20*time.Second)
	turns := len(s.turns("cirrus"))
	if hosts := allowedHosts(t, fake); slices.Contains(hosts, "registry.example.test:8443") || !slices.Contains(hosts, "hub.example.test") {
		t.Fatalf("the environment was made allowing %q", hosts)
	}

	var view cloudEgressSettings
	s.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": ".pkg.example.test"}, &view)
	hosts := allowedHosts(t, fake)
	if !slices.Contains(hosts, "*.pkg.example.test") || !slices.Contains(hosts, "pkg.example.test") {
		t.Errorf("after the edit the environment allows %q, want the domain and every name under it", hosts)
	}
	if view.Cloud == nil || len(view.Cloud.Environments) != 1 || view.Cloud.Environments[0].Loop != "cirrus" ||
		!view.Cloud.Environments[0].InSync || !slices.Equal(view.Cloud.NotApplied, []string{"registry.example.test:8443"}) {
		t.Fatalf("the view's cloud part = %+v, want cirrus in sync and the port entry not applied", view.Cloud)
	}

	fake.Mu.Lock()
	fake.RefuseUpdates = true
	fake.Mu.Unlock()
	view = cloudEgressSettings{}
	s.mustJSON("POST", "/api/settings/egress/hosts", map[string]string{"host": "mirror.example.test"}, &view)
	if environment := view.Cloud.Environments[0]; environment.InSync || environment.Error == "" {
		t.Errorf("after a refused write: %+v, want out of sync with the reason", environment)
	}
	if slices.Contains(allowedHosts(t, fake), "mirror.example.test") {
		t.Error("the refused write changed the environment")
	}
	var seen cloudEgressSettings
	if resp, body := s.asCookie("GET", "/api/settings/egress", s.signedInAs("mia", "member"), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("a member's read = %d %s", resp.StatusCode, body)
	} else if err := json.Unmarshal(body, &seen); err != nil {
		t.Fatal(err)
	}
	if environment := seen.Cloud.Environments[0]; environment.InSync || environment.Error != "" {
		t.Errorf("a member reads %+v, want out of sync with no reason: it quotes the cloud's answer, unredacted", environment)
	}

	fake.Mu.Lock()
	fake.RefuseUpdates = false
	fake.Mu.Unlock()
	view = cloudEgressSettings{}
	s.mustJSON("DELETE", "/api/settings/egress/hosts/"+url.PathEscape(".pkg.example.test"), nil, &view)
	if environment := view.Cloud.Environments[0]; !environment.InSync || environment.Error != "" {
		t.Errorf("after the next edit: %+v, want in sync", environment)
	}
	if hosts := allowedHosts(t, fake); slices.Contains(hosts, "pkg.example.test") || !slices.Contains(hosts, "mirror.example.test") {
		t.Errorf("after the next edit the environment allows %q", hosts)
	}
	if after := len(s.turns("cirrus")); after != turns {
		t.Errorf("the edits ran %d turns, want none", after-turns)
	}
}
