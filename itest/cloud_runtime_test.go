//go:build integration

package itest

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// The account token fakeclaude runs cloud sessions on: synthetic, never a
// real one.
const cloudFixtureToken = "sk-ant-oat01-fixture-cloud-account"

// cloudFixtureEnvironment is the environment the harness's cloud loops
// start in, as Settings → Cloud would have chosen it.
const cloudFixtureEnvironment = "env_01Fixture"

// startCloudHub starts a hub with one cloud loop and its account signed in,
// driven through the claude CLI, fakeclaude's --cloud (ADR-0059). Its
// public address is its own cloud listener on loopback, set through
// Settings → Cloud, so the address check passes and cloud loops wake: the
// harness's stand-in for a tunnel is the listener itself, which the loop
// home's hooks post to. args are the hub's besides.
func startCloudHub(t *testing.T, name string, args ...string) *server {
	t.Helper()
	s := startCloudHubAt(t, name, "", append([]string{"--cloud-listen", "127.0.0.1:0", "--cloud-loopback-address"}, args...)...)
	var view cloudSettings
	s.mustJSON("PUT", "/api/settings/cloud/address", map[string]string{"address": s.cloudURL}, &view)
	if view.Check == nil || view.Check.State != "ok" {
		t.Fatalf("the harness's public address %s checks as %+v, want ok", s.cloudURL, view.Check)
	}
	// the account check at boot is a login check, a CLI run of its own
	within(t, 20*time.Second, "the boot's account check", func() bool {
		s.mustJSON("GET", "/api/settings/cloud", nil, &view)
		return view.Account.Check != nil && view.Account.Check.State == "ok"
	})
	return s
}

// startCloudHubAt starts a hub with one cloud loop and its account signed
// in, with address stored as the public address, "" for none, and the hub
// started with args besides: the first start too, which seeds the stored
// egress allowlist from --egress-allow.
func startCloudHubAt(t *testing.T, name, address string, args ...string) *server {
	t.Helper()
	dataDir := t.TempDir()
	s := startServerArgs(t, dataDir, append([]string{"--runtime", "bare"}, args...)...)
	s.createLoop(name, nil)
	s.stop()
	makeCloudLoop(t, dataDir, name)
	signInCloud(t, dataDir, address)
	return startServerArgs(t, dataDir, append([]string{"--runtime", "bare"}, args...)...)
}

// signInCloud stores the synthetic account, the public address, "" for
// none, and the environment, as Settings → Cloud will.
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
	putDefaultLogin(t, db, store.CloudAccount{Token: cloudFixtureToken, OrgUUID: "00000000-0000-4000-8000-000000000001"})
	if err := db.Settings().Set(ctx, store.SettingCloudAddress, address); err != nil {
		t.Fatal(err)
	}
	environment, _ := json.Marshal(store.CloudEnvironment{ID: cloudFixtureEnvironment})
	if err := db.Settings().Set(ctx, store.SettingCloudEnvironment, string(environment)); err != nil {
		t.Fatal(err)
	}
}

// mcpInitialize is a JSON-RPC initialize, the first call a session's MCP
// client makes.
const mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"itest","version":"0"}}}`

// vmToken is the hub token the session's VM holds, as its loop home's
// .mcp.json carried it there: what a copy lifted out of that VM would be.
func vmToken(t *testing.T, s *server, vm string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.fkState, "cloud", vm, "repo", ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	token, _ := strings.CutPrefix(config.MCPServers["spool"].Headers["Authorization"], "Bearer ")
	return token
}

// A cloud loop's hub token outlives its sessions (ADR-0061), so an old VM
// of the loop holds a token that still works. What keeps it from binding
// the loop's next start is the session the start's CLI printed: here a
// rotation's next start holds its VM's SessionStart back, and the old
// VM posts one while the start is pending. It is refused, and the start
// takes its own VM's post.
func TestAStaleVMOfALoopCantBindItsNextStart(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "nimbus")
	s.message("nimbus", "first words")
	first := s.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	firstVM := cloudVMs(t, s)[0]
	stale := vmToken(t, s, firstVM)
	if stale == "" || stale != hubMCPToken(t, s, "nimbus") {
		t.Fatal("the first VM doesn't hold the loop's token")
	}

	held := filepath.Join(s.fkState, "cloud-start-held")
	if err := os.WriteFile(held, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.mustJSON("POST", "/api/loops/nimbus/rotate", nil, nil)
	within(t, 30*time.Second, "the rotation", func() bool { return strings.Contains(s.log(), "context rotated") })
	s.message("nimbus", "second words")
	within(t, 30*time.Second, "the next start's VM", func() bool { return len(cloudVMs(t, s)) == 2 })
	if got := postHook(t, s, stale, "session_01Stale"); got != http.StatusConflict {
		t.Errorf("the old VM's SessionStart during the new start: %d, want 409", got)
	}
	if !strings.Contains(s.log(), "didn't create") {
		t.Error("the refused post wasn't logged")
	}
	if err := os.Remove(held); err != nil {
		t.Fatal(err)
	}

	second := s.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "second words") })
	if second.IsError || second.SessionID == first.SessionID {
		t.Fatalf("the turn after the rotation: %+v, want one in a new session", second)
	}
	var viewed cloudLoopJSON
	s.mustJSON("GET", "/api/loops/nimbus", nil, &viewed)
	if strings.HasSuffix(viewed.CloudSessionURL, "session_01Stale") {
		t.Errorf("the stale post bound the loop to %s", viewed.CloudSessionURL)
	}
	if hubMCPToken(t, s, "nimbus") != stale {
		t.Error("the new session started on a new token: the token is the environment's, not the session's")
	}
}

// Stopping a cloud loop refuses its token at once, without waiting for a
// turn, and starting it again takes the same token, with nothing to set
// again (ADR-0061). A kill revokes the token for good: the loop is held,
// with a problem that says so, until Rotate token mints another, which
// it then wakes on.
func TestStoppingACloudLoopRefusesItsTokenAndAKillRevokesIt(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "stratus")
	s.message("stratus", "hello")
	first := s.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "hello") })
	token := hubMCPToken(t, s, "stratus")
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusOK {
		t.Fatalf("the loop's token before the stop: %d, want 200", got)
	}

	s.mustJSON("POST", "/api/loops/stratus/pause", nil, nil)
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("the token of a stopped loop: %d, want 401", got)
	}
	if got := postHook(t, s, token, "session_01Paused"); got != http.StatusUnauthorized {
		t.Errorf("a hook post on a stopped loop's token: %d, want 401", got)
	}
	if got := sessionEndReason(t, s, "stratus", first.SessionID); got != store.EndReasonPaused {
		t.Errorf("the stopped session ended as %q, want %q", got, store.EndReasonPaused)
	}
	s.mustJSON("POST", "/api/loops/stratus/resume", nil, nil)
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusOK {
		t.Errorf("the same token once the loop runs again: %d, want 200", got)
	}
	s.message("stratus", "again")
	again := s.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "again") })
	if again.IsError || again.SessionID == first.SessionID || len(cloudVMs(t, s)) != 2 {
		t.Fatalf("the wake after the stop: %+v in %d VMs, want a new session", again, len(cloudVMs(t, s)))
	}
	if hubMCPToken(t, s, "stratus") != token {
		t.Error("the new session started on a new token")
	}

	s.mustJSON("POST", "/api/loops/stratus/kill", nil, nil)
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("the killed loop's token: %d, want 401", got)
	}
	if killed := s.waitHealth("stratus", "failing"); len(killed.Problems) != 1 || killed.Problems[0].Symptom != store.SymptomCloudSetup {
		t.Fatalf("problems after the kill = %+v, want one cloud_setup", killed.Problems)
	}
	s.message("stratus", "held words")
	time.Sleep(time.Second)
	if len(cloudVMs(t, s)) != 2 {
		t.Fatal("a killed loop started a session before its token was rotated")
	}

	resp, body := s.do("POST", "/api/loops/stratus/cloud/token", nil)
	rotated := string(body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") ||
		resp.Header.Get("Cache-Control") != "no-store" || rotated == "" || rotated == token || rotated != hubMCPToken(t, s, "stratus") {
		t.Fatalf("Rotate token answered %d %q, want the new token, uncached", resp.StatusCode, resp.Header)
	}
	held := s.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })
	if held.IsError {
		t.Fatalf("the held message's turn: %+v", held)
	}
	if after := s.waitHealth("stratus", "ok"); len(after.Problems) != 0 {
		t.Errorf("problems after the rotation = %+v, want none", after.Problems)
	}
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("the killed token after the rotation: %d, want 401", got)
	}
	if strings.Contains(s.log(), rotated) {
		t.Error("the rotated token is in the hub's log")
	}
}

// A stop whose status wasn't stored, and a kill whose revocation failed,
// say so, rather than answering as if the session were cut off: its
// token still reaches the hub (#1114). Doing it again once the store
// takes the write cuts it off.
func TestACloudLoopsStopSaysWhenItsTokenWasNotRevoked(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	hub.message("cirrus", "hello")
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "hello") })
	token := hubMCPToken(t, hub, "cirrus")

	db, err := sql.Open("sqlite", "file:"+filepath.Join(hub.dataDir, "spool.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for verb, column := range map[string]string{"pause": "status", "kill": "hub_mcp_token"} {
		if _, err := db.Exec(`CREATE TRIGGER refuse_write BEFORE UPDATE OF ` + column + ` ON loops
			BEGIN SELECT RAISE(ABORT, 'the test refuses the write'); END`); err != nil {
			t.Fatal(err)
		}
		resp, body := hub.do("POST", "/api/loops/cirrus/"+verb, nil)
		if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "can still reach the hub") {
			t.Errorf("a %s whose write failed = %d %s, want a 500 that says so", verb, resp.StatusCode, body)
		}
		if got := postTo(t, hub.cloudURL, token, mcpInitialize); got != http.StatusOK {
			t.Fatalf("the token after a failed %s: %d, want it still working, as the answer said", verb, got)
		}
		if _, err := db.Exec(`DROP TRIGGER refuse_write`); err != nil {
			t.Fatal(err)
		}
	}

	hub.mustJSON("POST", "/api/loops/cirrus/kill", nil, nil)
	if got := postTo(t, hub.cloudURL, token, mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("the token after the retried kill: %d, want 401", got)
	}
}

// Deleting a cloud loop revokes its hub token. The CLI can't archive the
// session, which is left for the operator to archive on claude.ai.
func TestDeletingACloudLoopRevokesItsToken(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "cumulus")
	s.message("cumulus", "hello")
	s.waitTurn("cumulus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "hello") })
	token := hubMCPToken(t, s, "cumulus")
	s.mustJSON("DELETE", "/api/loops/cumulus", nil, nil)
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("the deleted loop's token: %d, want 401", got)
	}
}

// A cloud address the loop's token would cross in the clear is refused at
// use: the loop doesn't wake, and no session starts, whatever wrote the
// address.
func TestCloudLoopDoesNotWakeOnAPlainHTTPAddress(t *testing.T) {
	t.Parallel()
	s := startCloudHubAt(t, "cumulus", "http://hub.example.test")
	s.message("cumulus", "hello")
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(s.log(), "must be https"); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the hub log doesn't say why the loop didn't wake:\n%s", s.log())
		}
	}
	if vms := cloudVMs(t, s); len(vms) != 0 {
		t.Fatalf("a plain-HTTP address started %d sessions", len(vms))
	}
}

// While the public address fails its check, cloud loops don't wake: a
// message waits in the stored inbox, no session starts, and the loop
// carries a cloud_address problem that reads as failing. The check that
// passes again clears the problem and wakes the loop with what waited
// (ADR-0056).
func TestCloudLoopsWaitWhileTheAddressFailsItsCheck(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "cirrus")

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
	if vms := cloudVMs(t, s); len(vms) != 0 {
		t.Fatalf("a held cloud loop started %d sessions, want none", len(vms))
	}

	s.mustJSON("PUT", "/api/settings/cloud/address", map[string]string{"address": s.cloudURL}, &view)
	if view.Check == nil || view.Check.State != "ok" {
		t.Fatalf("the address set back checks as %+v, want ok", view.Check)
	}
	s.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })
	if after := s.waitHealth("cirrus", "ok"); len(after.Problems) != 0 {
		t.Fatalf("after the check passed: problems %+v, want none", after.Problems)
	}
}

// The hub's check of its public address can pass where a cloud session
// can't reach the hub: over a tailnet, the address resolves on the hub's
// machine to a private route (#1093). Its pass says so, and a session whose
// start never reaches the hub has the last word: step 2 turns red, every
// cloud loop is held with a cloud_address problem, and only a check the
// operator asks for lets them go (ADR-0056).
func TestASessionThatCantReachTheHubHoldsCloudLoops(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus", "--cloud-start-within-sec", "3")
	var view cloudSettings
	hub.mustJSON("GET", "/api/settings/cloud", nil, &view)
	if view.Check == nil || view.Check.State != "ok" || view.Check.Route != "private" {
		t.Fatalf("a pass over loopback checks as %+v, want ok over a private route", view.Check)
	}

	// the CLI exits once it has created the session, as CLI 2.1.310 does
	if err := os.WriteFile(filepath.Join(hub.fkState, "cloud-start-exits"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	refused := filepath.Join(hub.fkState, "cloud-egress-refused")
	if err := os.WriteFile(refused, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hub.message("cirrus", "unheard words")
	within(t, 30*time.Second, "step 2 to take the session's verdict", func() bool {
		hub.mustJSON("GET", "/api/settings/cloud", nil, &view)
		return view.Check != nil && view.Check.Stage == "session"
	})
	if view.Check.State != "failed" {
		t.Errorf("the session's verdict reads as %+v, want failed", view.Check)
	}
	held := hub.waitHealth("cirrus", "failing")
	if len(held.Problems) != 1 || held.Problems[0].Symptom != "cloud_address" {
		t.Fatalf("problems = %+v, want one cloud_address", held.Problems)
	}

	vms := len(cloudVMs(t, hub))
	before := len(hub.turns("cirrus"))
	hub.message("cirrus", "held words")
	time.Sleep(2 * time.Second)
	if after := len(hub.turns("cirrus")); after != before {
		t.Fatalf("a held cloud loop ran %d turns, want none", after-before)
	}
	if after := len(cloudVMs(t, hub)); after != vms {
		t.Fatalf("a held cloud loop started %d sessions, want none", after-vms)
	}

	if err := os.Remove(refused); err != nil {
		t.Fatal(err)
	}
	hub.mustJSON("POST", "/api/settings/cloud/check", nil, &view)
	if view.Check == nil || view.Check.State != "ok" {
		t.Fatalf("the check asked for from the page: %+v, want ok", view.Check)
	}
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })
	if after := hub.waitHealth("cirrus", "ok"); len(after.Problems) != 0 {
		t.Fatalf("after the check passed: problems %+v, want none", after.Problems)
	}
}

// A SessionStart post the hub refuses still came from the session, so it
// reached the hub: here its id is of a shape the hub doesn't know, as a
// change of the CLI's ids would make it (#1099). The start fails, but it
// is no verdict on the address, and the loop isn't held (#1093).
func TestARefusedSessionStartIsNoVerdictOnTheAddress(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "nimbus", "--cloud-start-within-sec", "3")
	for _, instrument := range []string{"cloud-start-exits", "cloud-start-held"} {
		if err := os.WriteFile(filepath.Join(hub.fkState, instrument), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hub.message("nimbus", "first words")
	within(t, 10*time.Second, "the start's VM", func() bool { return len(cloudVMs(t, hub)) == 1 })
	token := vmToken(t, hub, cloudVMs(t, hub)[0])
	if got := postHook(t, hub, token, "cse_not-a-known-shape"); got != http.StatusBadRequest {
		t.Fatalf("a SessionStart with an id of an unknown shape: %d, want 400", got)
	}
	hub.waitTurn("nimbus", 30*time.Second, func(turn turn) bool {
		return turn.IsError && strings.Contains(turn.ResultText, "no session started within 3s")
	})

	var view cloudSettings
	hub.mustJSON("GET", "/api/settings/cloud", nil, &view)
	if view.Check == nil || view.Check.State != "ok" || view.Check.Stage == "session" {
		t.Errorf("after a start whose session posted: step 2 is %+v, want it still ok", view.Check)
	}
	for _, problem := range hub.loop("nimbus").Problems {
		if problem.Symptom == "cloud_address" {
			t.Errorf("a start whose session posted held the loop: %+v", problem)
		}
	}
}
