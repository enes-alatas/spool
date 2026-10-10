//go:build integration

package itest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// signedInAs adds a user with role through `spool user`, signs them in and
// sets their own password, and returns the session cookie.
func (s *server) signedInAs(name, role string) string {
	s.t.Helper()
	oneTime := oneTimePassword(s.t, s.spoolUser("add", name, "--role", role))
	session, _ := s.signIn(name, oneTime)
	if resp, body := s.asCookie("POST", "/api/me/password", session,
		map[string]string{"new_password": name + "'s own password"}); resp.StatusCode != http.StatusOK {
		s.t.Fatalf("%s's change = %d %s", name, resp.StatusCode, body)
	}
	return session
}

// A member reads the fleet and talks to its loops, and is refused 403
// forbidden_role by anything that creates, changes or operates one. An
// admin and the operator token do both (ADR-0048).
func TestRolesBoundWhatAUserMayDo(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	srv.createLoop("scout", nil)
	member := srv.signedInAs("mia", "member")
	admin := srv.signedInAs("ada", "admin")

	for _, call := range []struct{ method, path string }{
		{"GET", "/api/loops"},
		{"GET", "/api/loops/scout"},
		{"POST", "/api/loops/scout/message"},
	} {
		var body any
		if call.method == "POST" {
			body = map[string]string{"text": "hello from a member"}
		}
		if resp, out := srv.asCookie(call.method, call.path, member, body); resp.StatusCode >= 300 {
			t.Errorf("member %s %s = %d %s; want it allowed", call.method, call.path, resp.StatusCode, out)
		}
	}
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/loops", map[string]any{"name": "intruder", "mission": "m"}},
		{"PATCH", "/api/loops/scout", map[string]any{"mission": "changed"}},
		{"POST", "/api/loops/scout/pause", nil},
		{"DELETE", "/api/loops/scout", nil},
		{"PUT", "/api/settings", map[string]any{}},
	} {
		resp, out := srv.asCookie(call.method, call.path, member, call.body)
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(out), `"forbidden_role"`) {
			t.Errorf("member %s %s = %d %s; want 403 forbidden_role", call.method, call.path, resp.StatusCode, out)
		}
	}
	if loop := srv.loop("scout"); loop.Mission == "changed" {
		t.Error("a member's refused edit changed the loop")
	}

	// A member's message carries their name, even one that claims to be the
	// operator's, so the loop never takes it for the operator's.
	if resp, out := srv.asCookie("POST", "/api/loops/scout/message", member,
		map[string]string{"author": "operator", "text": "posing as the operator"}); resp.StatusCode >= 300 {
		t.Fatalf("member message = %d %s", resp.StatusCode, out)
	}
	var thread []struct {
		Author string `json:"author"`
		Text   string `json:"text"`
	}
	srv.mustJSON("GET", "/api/loops/scout/conversation", nil, &thread)
	for _, msg := range thread {
		if msg.Text == "posing as the operator" && msg.Author != "mia" {
			t.Errorf("a member's message is stored as %q; want mia", msg.Author)
		}
	}

	if resp, out := srv.asCookie("PATCH", "/api/loops/scout", admin, map[string]any{"mission": "an admin's mission"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin PATCH = %d %s; want it allowed", resp.StatusCode, out)
	}
	if resp, out := srv.do("POST", "/api/loops/scout/pause", nil); resp.StatusCode >= 300 {
		t.Fatalf("the operator token's pause = %d %s; want it allowed", resp.StatusCode, out)
	}
}

// A member reads no owner DM: not in the loop's conversation, not in
// Activity, not as the file one carried, and not in the loop's raw
// transcript, which is closed to them whole. The operator token reads all
// of it (ADR-0048).
func TestAMemberReadsNoOwnerDM(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6262, First: "Operator", Username: "operator"}
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "shot.png"), pngOf(t, 8, 8))
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws, "workspace_mode": "dir"})
	tg.dm("alpha", operator, "a private word for the owner")
	envelopeWith(t, srv, "alpha", "a private word for the owner")
	sess := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	if res := callSend(t, sess, map[string]any{"destination": "owner_dm", "text": "the private shot", "attach": "shot.png"}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	member := srv.signedInAs("mia", "member")

	var dms []struct {
		Text        string `json:"text"`
		Attachments []struct {
			ID int64 `json:"id"`
		} `json:"attachments"`
	}
	srv.mustJSON("GET", "/api/loops/alpha/conversation?conversation=owner_dm", nil, &dms)
	var attachment int64
	for _, dm := range dms {
		for _, file := range dm.Attachments {
			attachment = file.ID
		}
	}
	if len(dms) < 2 || attachment == 0 {
		t.Fatalf("the token reads owner DMs %+v; want both, one with the file", dms)
	}

	if resp, body := srv.asCookie("GET", "/api/loops/alpha/conversation?conversation=owner_dm", member, nil); resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("a member's owner-DM conversation = %d %s; want it empty", resp.StatusCode, body)
	}
	if resp, body := srv.asCookie("GET", "/api/activity", member, nil); resp.StatusCode != http.StatusOK || strings.Contains(string(body), "private") {
		t.Errorf("a member's Activity = %d %s; want no owner DM in it", resp.StatusCode, body)
	}
	if _, body := srv.do("GET", "/api/activity", nil); !strings.Contains(string(body), "a private word for the owner") {
		t.Errorf("the token's Activity holds no owner DM: %s", body)
	}
	if resp, _ := srv.asCookie("GET", fmt.Sprintf("/api/attachments/%d", attachment), member, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a member's GET of the owner DM's file = %d; want 404", resp.StatusCode)
	}
	if resp, _ := srv.do("GET", fmt.Sprintf("/api/attachments/%d", attachment), nil); resp.StatusCode != http.StatusOK {
		t.Errorf("the token's GET of the owner DM's file = %d; want 200", resp.StatusCode)
	}
	for _, path := range []string{"/api/loops/alpha/events", "/api/loops/alpha/turns"} {
		if resp, _ := srv.asCookie("GET", path, member, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("a member's GET %s = %d; want 403", path, resp.StatusCode)
		}
	}
}

// A member reads who is waiting on the Access page, but no pending
// sender's pairing code: the code is how an admin knows the person
// vouching for a sender got the bot's DM. An admin and the operator token
// read every code (#689).
func TestAMemberReadsNoPairingCode(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	srv := startServer(t, dataDir)
	srv.stop()
	seedSlackSender(t, dataDir, "U0ALICE", "T0ACME", "Alice", "pending")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tg_senders
		(tg_user_id, username, display, status, pair_code, first_seen_via, created_at, updated_at)
		VALUES (4242,'bob','Bob','pending','654321','dm:alpha',1,1)`); err != nil {
		t.Fatalf("seed bob: %v", err)
	}
	db.Close()
	srv = startServer(t, dataDir)
	member := srv.signedInAs("mia", "member")
	admin := srv.signedInAs("ada", "admin")

	code := func(path, cookie string) string {
		t.Helper()
		var resp *http.Response
		var body []byte
		if cookie == "" {
			resp, body = srv.do("GET", path, nil)
		} else {
			resp, body = srv.asCookie("GET", path, cookie, nil)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, body)
		}
		var senders []struct {
			PairCode string `json:"pair_code"`
		}
		if err := json.Unmarshal(body, &senders); err != nil || len(senders) != 1 {
			t.Fatalf("GET %s = %s; want the one seeded sender", path, body)
		}
		return senders[0].PairCode
	}
	for path, want := range map[string]string{"/api/telegram/senders": "654321", "/api/slack/senders": "123456"} {
		if got := code(path, member); got != "" {
			t.Errorf("a member's %s carries pairing code %q; want none", path, got)
		}
		if got := code(path, admin); got != want {
			t.Errorf("an admin's %s carries pairing code %q; want %s", path, got, want)
		}
		if got := code(path, ""); got != want {
			t.Errorf("the token's %s carries pairing code %q; want %s", path, got, want)
		}
	}
}
