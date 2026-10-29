//go:build integration

package itest

import (
	"context"
	"database/sql"
	"io/fs"
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

// cloudFixtureEnvironment is an environment a cloud loop the API creates
// names.
const cloudFixtureEnvironment = "env_01Fixture"

// startCloudHub starts a hub with one cloud loop and its account signed in,
// driven through the claude CLI, fakeclaude's --cloud (ADR-0059). Its
// public address is its own cloud listener on loopback, set through
// Settings → Cloud, so the address check passes and cloud loops wake: the
// harness's stand-in for a tunnel is the listener itself, which the
// session's hooks post to, and the loop's environment is set up for it.
// args are the hub's besides.
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
	setUpCloudEnvironment(t, s, cloudEnvironmentOf(name), cloudTokenOf(name))
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
	putDefaultLogin(t, db, store.CloudAccount{Token: cloudFixtureToken, OrgUUID: "00000000-0000-4000-8000-000000000001"})
	if err := db.Settings().Set(ctx, store.SettingCloudAddress, address); err != nil {
		t.Fatal(err)
	}
}

// mcpInitialize is a JSON-RPC initialize, the first call a session's MCP
// client makes.
const mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"itest","version":"0"}}}`

// vmHolds reports whether any file in the session's VM holds value: the
// hub token never enters it (ADR-0061), so a copy can't be lifted out.
func vmHolds(t *testing.T, s *server, vm, value string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(filepath.Join(s.fkState, "cloud", vm), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		found = found || strings.Contains(string(raw), value)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
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
	stale := cloudTokenOf("nimbus")
	if vmHolds(t, s, firstVM, stale) {
		t.Fatal("the first VM holds the loop's token: it is the environment's secret, which the proxy adds")
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
	if got := postHook(t, s, stale, "session_01Probe"); got != http.StatusConflict {
		t.Errorf("the loop's token after the new start: %d, want 409 (taken, and no start pending): the token is the environment's, not the session's", got)
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
	token := cloudTokenOf("stratus")
	if got := postHook(t, s, token, "session_01Probe"); got != http.StatusConflict {
		t.Fatalf("the loop's token before the stop: %d, want 409 (taken, and no start pending)", got)
	}

	s.mustJSON("POST", "/api/loops/stratus/pause", nil, nil)
	if got := postHook(t, s, token, "session_01Probe"); got != http.StatusUnauthorized {
		t.Errorf("the token of a stopped loop: %d, want 401", got)
	}
	if got := postHook(t, s, token, "session_01Paused"); got != http.StatusUnauthorized {
		t.Errorf("a hook post on a stopped loop's token: %d, want 401", got)
	}
	if got := sessionEndReason(t, s, "stratus", first.SessionID); got != store.EndReasonPaused {
		t.Errorf("the stopped session ended as %q, want %q", got, store.EndReasonPaused)
	}
	s.mustJSON("POST", "/api/loops/stratus/resume", nil, nil)
	if got := postHook(t, s, token, "session_01Probe"); got != http.StatusConflict {
		t.Errorf("the same token once the loop runs again: %d, want 409 (taken, and no start pending)", got)
	}
	s.message("stratus", "again")
	again := s.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "again") })
	if again.IsError || again.SessionID == first.SessionID || len(cloudVMs(t, s)) != 2 {
		t.Fatalf("the wake after the stop: %+v in %d VMs, want a new session", again, len(cloudVMs(t, s)))
	}
	if got := postHook(t, s, token, "session_01Probe"); got != http.StatusConflict {
		t.Errorf("the token after the new session: %d, want 409 (taken, and no start pending): the new session runs on the same token", got)
	}

	s.mustJSON("POST", "/api/loops/stratus/kill", nil, nil)
	if got := postHook(t, s, token, "session_01Probe"); got != http.StatusUnauthorized {
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
		resp.Header.Get("Cache-Control") != "no-store" || rotated == "" || rotated == token || hubMCPToken(t, s, "stratus") != "" {
		t.Fatalf("Rotate token answered %d %q, want the new token, uncached, and kept only as its hash", resp.StatusCode, resp.Header)
	}
	if view := s.loop("stratus"); view.Status != "paused" {
		t.Errorf("after Rotate token the loop is %q, want it stopped until its new secret is set", view.Status)
	}
	// the operator sets it as the environment's secret, then starts the loop
	setUpCloudEnvironment(t, s, cloudEnvironmentOf("stratus"), rotated)
	s.mustJSON("POST", "/api/loops/stratus/resume", nil, nil)
	if got := postHook(t, s, rotated, "session_01Probe"); got != http.StatusConflict {
		t.Errorf("the rotated token once the loop runs: %d, want 409 (taken, and no start pending)", got)
	}
	held := s.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })
	if held.IsError {
		t.Fatalf("the held message's turn: %+v", held)
	}
	if after := s.waitHealth("stratus", "ok"); len(after.Problems) != 0 {
		t.Errorf("problems after the rotation = %+v, want none", after.Problems)
	}
	if got := postHook(t, s, token, "session_01Probe"); got != http.StatusUnauthorized {
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
	token := cloudTokenOf("cirrus")

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
		if got := postHook(t, hub, token, "session_01Probe"); got != http.StatusConflict {
			t.Fatalf("the token after a failed %s: %d, want it still working, as the answer said", verb, got)
		}
		if _, err := db.Exec(`DROP TRIGGER refuse_write`); err != nil {
			t.Fatal(err)
		}
	}

	hub.mustJSON("POST", "/api/loops/cirrus/kill", nil, nil)
	if got := postHook(t, hub, token, "session_01Probe"); got != http.StatusUnauthorized {
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
	token := cloudTokenOf("cumulus")
	s.mustJSON("DELETE", "/api/loops/cumulus", nil, nil)
	if got := postHook(t, s, token, "session_01Probe"); got != http.StatusUnauthorized {
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
	token := cloudTokenOf("nimbus")
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

// A cloud loop's /mcp takes only its own session, by the session ID the
// setup script has its MCP client send, and only while a turn of it runs
// or just after (ADR-0061): every session in the loop's environment holds
// the token, a session someone starts there by hand included. Here the
// VM's own client sends through the spool server the setup script wrote,
// on the environment's network secret.
func TestACloudLoopsMCPTakesOnlyItsSessionInATurn(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "altus")
	hub.message("altus", `!send {"destination":"control_room","text":"hello from the vm"}`)
	sent := hub.waitTurn("altus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "sent") })
	if sent.IsError || strings.Contains(sent.ResultText, "send error") {
		t.Fatalf("the VM's send: %+v", sent)
	}
	if len(hub.activityWith("hello from the vm")) == 0 {
		t.Fatal("the VM's send was answered but never stored")
	}

	token := cloudTokenOf("altus")
	var viewed cloudLoopJSON
	hub.mustJSON("GET", "/api/loops/altus", nil, &viewed)
	session := viewed.CloudSessionURL[strings.LastIndex(viewed.CloudSessionURL, "/")+1:]
	if !strings.HasPrefix(session, "session_") {
		t.Fatalf("the loop's session is %q", viewed.CloudSessionURL)
	}
	// the turn ended a moment ago, within its grace
	if got := postToAs(t, hub.cloudURL, token, "cse_"+strings.TrimPrefix(session, "session_"), mcpInitialize); got != http.StatusOK {
		t.Errorf("the loop's session just after its turn: %d, want 200", got)
	}
	if got := postToAs(t, hub.cloudURL, token, "cse_01ByHand", mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("a session started by hand in the loop's environment: %d, want 401", got)
	}
	if got := postTo(t, hub.cloudURL, token, mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("a call naming no session: %d, want 401", got)
	}
	if !strings.Contains(hub.log(), "cloud mcp call refused") {
		t.Error("the refused calls weren't logged")
	}
}

// Past a turn's grace, the loop's own session can still connect and list
// the hub's tools, since a client refused between turns stays failed
// into the next (live checks on #1190), and a tool call is refused as a
// tool error naming why. The next turn's send goes through (ADR-0061).
func TestACloudSessionsMCPReconnectsBetweenTurns(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "stratus")
	hub.message("stratus", "first words")
	hub.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	hub.waitState("stratus", "asleep", 20*time.Second)
	var viewed cloudLoopJSON
	hub.mustJSON("GET", "/api/loops/stratus", nil, &viewed)
	session := "cse_" + strings.TrimPrefix(viewed.CloudSessionURL[strings.LastIndex(viewed.CloudSessionURL, "/")+1:], "session_")
	token := cloudTokenOf("stratus")

	// the hub's grace after a turn is a minute
	time.Sleep(62 * time.Second)
	const listTools = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	for _, body := range []string{mcpInitialize, listTools} {
		if status, answer := mcpAnswer(t, hub.cloudURL, token, session, body); status != http.StatusOK || !strings.Contains(answer, `"result"`) {
			t.Errorf("the loop's session between turns: %d %s, want it answered", status, answer)
		}
	}
	const send = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send_message","arguments":{"destination":"control_room","text":"between turns"}}}`
	if status, answer := mcpAnswer(t, hub.cloudURL, token, session, send); status != http.StatusOK ||
		!strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "no turn of this loop is in flight") {
		t.Errorf("a send between turns: %d %s, want a tool error naming the turn", status, answer)
	}
	if len(hub.activityWith("between turns")) != 0 {
		t.Error("a send between turns was stored")
	}
	if got := postToAs(t, hub.cloudURL, token, "cse_01ByHand", mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("a session started by hand, between turns: %d, want 401", got)
	}

	hub.message("stratus", `!send {"destination":"control_room","text":"from the next turn"}`)
	sent := hub.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "sent") })
	if sent.IsError || strings.Contains(sent.ResultText, "send error") || len(hub.activityWith("from the next turn")) == 0 {
		t.Errorf("the next turn's send: %+v, want it stored", sent)
	}
}

// A cloud loop's session attaches only the repos the loop names: the
// setup script's guard asks the hub about each add_repo call, which
// takes only the loop's repos, and its deny rules refuse the
// claude-code-remote tools that act on the account (ADR-0061). The
// prompt names the loop's other repos for it to attach.
func TestACloudLoopsAddRepoIsHeldToItsRepos(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	hub.mustJSON("POST", "/api/loops/cirrus/pause", nil, nil)
	hub.mustJSON("PATCH", "/api/loops/cirrus", map[string]any{
		"cloud_repo": "example-org/cirrus", "cloud_branch": "main", "cloud_repos": []string{"example-org/tools"},
		"cloud_environment_id": cloudEnvironmentOf("cirrus"),
	}, nil)
	hub.mustJSON("POST", "/api/loops/cirrus/resume", nil, nil)

	answered := map[string]bool{}
	ask := func(line string) string {
		hub.message("cirrus", line)
		answer := hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool {
			return !answered[turn.ID] && strings.Contains(turn.ResultText, "tool ")
		})
		answered[answer.ID] = true
		return answer.ResultText
	}
	if got := ask(`!tool mcp__claude-code-remote__add_repo {"owner":"example-org","repo":"tools","access":"push"}`); got != "tool mcp__claude-code-remote__add_repo: called" {
		t.Errorf("add_repo for the loop's other repo: %q, want it called", got)
	}
	got := ask(`!tool mcp__claude-code-remote__add_repo {"owner":"example-org","repo":"payroll","access":"push"}`)
	if !strings.Contains(got, "refused") || !strings.Contains(got, "example-org/payroll is not one of this loop's repos") {
		t.Errorf("add_repo for a repo the loop doesn't name: %q, want it refused with the hub's reason", got)
	}
	if !strings.Contains(hub.log(), "cloud hook refused") {
		t.Error("the refused add_repo wasn't logged")
	}
	if got := ask(`!tool mcp__claude-code-remote__create_trigger {}`); got != "tool mcp__claude-code-remote__create_trigger: denied" {
		t.Errorf("create_trigger: %q, want it denied", got)
	}

	vms := cloudVMs(t, hub)
	context, err := os.ReadFile(filepath.Join(hub.fkState, "cloud", vms[len(vms)-1], "context"))
	if err != nil || !strings.Contains(string(context), "Your other repos are example-org/tools.") {
		t.Errorf("the session's context (%v) doesn't name the loop's other repos:\n%s", err, context)
	}
}
