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
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/runtime/cloud/cloudfake"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// The account token the fake API admits: synthetic, never a real one.
const cloudFixtureToken = "sk-ant-oat01-fixture-cloud-account"

// startCloudHub starts a hub with one cloud loop and its account signed in,
// driven through a fake of Anthropic's sessions API. Its public address is
// its own cloud listener on loopback, set through Settings → Cloud, so the
// address check passes and cloud loops wake: the harness's stand-in for a
// tunnel is the listener itself. The fake API never connects to it. args
// are the hub's besides.
func startCloudHub(t *testing.T, name string, args ...string) (*server, *cloudfake.Server) {
	t.Helper()
	s, fake := startCloudHubAt(t, name, "", append([]string{"--cloud-listen", "127.0.0.1:0", "--cloud-loopback-address"}, args...)...)
	var view cloudSettings
	s.mustJSON("PUT", "/api/settings/cloud/address", map[string]string{"address": s.cloudURL}, &view)
	if view.Check == nil || view.Check.State != "ok" {
		t.Fatalf("the harness's public address %s checks as %+v, want ok", s.cloudURL, view.Check)
	}
	return s, fake
}

// startCloudHubAt starts a hub with one cloud loop and its account signed
// in, with address stored as the public address, "" for none, and the hub
// started with args besides: the first start too, which seeds the stored
// egress allowlist from --egress-allow.
func startCloudHubAt(t *testing.T, name, address string, args ...string) (*server, *cloudfake.Server) {
	t.Helper()
	fake := cloudfake.New(cloudFixtureToken)
	t.Cleanup(fake.Close)
	dataDir := t.TempDir()
	s := startServerArgs(t, dataDir, append([]string{"--runtime", "bare"}, args...)...)
	s.createLoop(name, nil)
	s.stop()
	makeCloudLoop(t, dataDir, name)
	signInCloud(t, dataDir, address)
	return startServerArgs(t, dataDir, append([]string{"--runtime", "bare", "--cloud-api-base", fake.URL}, args...)...), fake
}

// signInCloud stores the synthetic account and the public address, "" for
// none, as Settings → Cloud will.
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
	if !strings.Contains(script, "'"+s.cloudURL+"/mcp'") || !strings.Contains(script, "${SPOOL_HUB_TOKEN}") {
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

// Deleting a cloud loop revokes its hub token first, then archives its
// session and its environment, so nothing starts in either again. The
// archive calls can take up to a minute, or fail, and the token works at
// no point in between (ADR-0056).
func TestDeletingACloudLoopRevokesItsTokenThenArchives(t *testing.T) {
	t.Parallel()
	s, fake := startCloudHub(t, "stratus")
	s.message("stratus", "hello")
	s.waitTurn("stratus", 20*time.Second, func(turn turn) bool { return turn.Trigger != store.TriggerTick })
	token := hubMCPToken(t, s, "stratus")
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"itest","version":"0"}}}`
	if got := postTo(t, s.cloudURL, token, initialize); got != http.StatusOK {
		t.Fatalf("the loop's token before the delete: %d, want 200", got)
	}

	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	// a held call keeps the fake from closing
	t.Cleanup(release)
	fake.Mu.Lock()
	fake.ArchiveGate = gate
	fake.Mu.Unlock()
	deleted := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodDelete, s.baseURL+"/api/loops/stratus", nil)
		req.Header.Set("Authorization", "Bearer "+s.operatorToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			deleted <- 0
			return
		}
		resp.Body.Close()
		deleted <- resp.StatusCode
	}()
	within(t, 10*time.Second, "the delete's first archive call", func() bool {
		fake.Mu.Lock()
		defer fake.Mu.Unlock()
		return fake.ArchivesHeld > 0
	})
	if got := postTo(t, s.cloudURL, token, initialize); got != http.StatusUnauthorized {
		t.Errorf("the loop's token while its archive is held: %d, want 401", got)
	}
	fake.Mu.Lock()
	fake.ArchiveGate = nil
	fake.Mu.Unlock()
	release()
	if status := <-deleted; status != http.StatusOK {
		t.Fatalf("the delete answered %d, want 200", status)
	}

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
	s, fake := startCloudHub(t, "cirrus", "--egress-allow", "registry.example.test:8443")

	s.message("cirrus", "make the environment")
	s.waitTurn("cirrus", 20*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "make the environment") })
	s.waitState("cirrus", "asleep", 20*time.Second)
	turns := len(s.turns("cirrus"))
	if hosts := allowedHosts(t, fake); slices.Contains(hosts, "registry.example.test:8443") || !slices.Contains(hosts, "127.0.0.1") {
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

// While the public address fails its check, cloud loops don't wake: a
// message waits in the stored inbox, nothing reaches the sessions API, and
// the loop carries a cloud_address problem that reads as failing. The
// check that passes again clears the problem and wakes the loop with what
// waited (ADR-0056).
func TestCloudLoopsWaitWhileTheAddressFailsItsCheck(t *testing.T) {
	t.Parallel()
	s, fake := startCloudHub(t, "cirrus")

	// The API answers the check's path too, with no value of its own.
	var view cloudSettings
	s.mustJSON("PUT", "/api/settings/cloud/address", map[string]string{"address": s.baseURL}, &view)
	if view.Check == nil || view.Check.Stage != "other_server" {
		t.Fatalf("the address reaching another server checks as %+v, want other_server", view.Check)
	}
	held := s.waitHealth("cirrus", "failing")
	if len(held.Problems) != 1 || held.Problems[0].Symptom != "cloud_address" || held.Problems[0].Since == 0 {
		t.Fatalf("problems = %+v, want one cloud_address with a since", held.Problems)
	}

	before := len(s.turns("cirrus"))
	s.message("cirrus", "held words")
	time.Sleep(2 * time.Second)
	if after := len(s.turns("cirrus")); after != before {
		t.Fatalf("a held cloud loop ran %d turns, want none", after-before)
	}
	fake.Mu.Lock()
	sessions := len(fake.Sessions)
	fake.Mu.Unlock()
	if sessions != 0 {
		t.Fatalf("a held cloud loop made %d sessions, want none", sessions)
	}

	s.mustJSON("PUT", "/api/settings/cloud/address", map[string]string{"address": s.cloudURL}, &view)
	if view.Check == nil || view.Check.State != "ok" {
		t.Fatalf("the address set back checks as %+v, want ok", view.Check)
	}
	s.waitTurn("cirrus", 20*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })
	if after := s.waitHealth("cirrus", "ok"); len(after.Problems) != 0 {
		t.Fatalf("after the check passed: problems %+v, want none", after.Problems)
	}
}
