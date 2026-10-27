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

type claudeLoginJSON struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Owner    string `json:"owner"`
	SignedIn bool   `json:"signed_in"`
	Default  bool   `json:"default"`
}

func (srv *server) claudeLogins() map[string]claudeLoginJSON {
	srv.t.Helper()
	var view struct {
		Logins []claudeLoginJSON `json:"logins"`
	}
	srv.mustJSON("GET", "/api/claude-logins", nil, &view)
	byName := map[string]claudeLoginJSON{}
	for _, login := range view.Logins {
		byName[login.Name] = login
	}
	return byName
}

// signInClaudeLogin signs the login in through fakeclaude's auth login,
// as Settings does, with the code for the fake's account named account,
// "" for its first.
func (srv *server) signInClaudeLogin(name, account string) {
	srv.t.Helper()
	var started struct {
		Link string `json:"link"`
	}
	srv.mustJSON("POST", "/api/claude-logins/"+name+"/sign-in", nil, &started)
	if !strings.Contains(started.Link, "oauth/authorize") {
		srv.t.Fatalf("%s's sign-in link = %q, want the CLI's authorize link", name, started.Link)
	}
	code := "fixture-code"
	if account != "" {
		code += "-" + account
	}
	srv.mustJSON("POST", "/api/claude-logins/"+name+"/sign-in/code", map[string]string{"code": code + "#fixture-state"}, nil)
}

// openStoreAt opens a stopped hub's store.
func openStoreAt(t *testing.T, dataDir string) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// A hub holds named Claude logins (ADR-0060): the default is listed before
// anyone signs it in, another is added under a name and signed in with the
// CLI's own login, and each keeps the address it signed in with. Each is
// refreshed on its own. The default can't be removed while loops run on
// it, and removing another revokes it at the source.
func TestTheHubHoldsSeveralClaudeLogins(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	hub := startServerArgs(t, dataDir, "--runtime", "bare")

	if logins := hub.claudeLogins(); len(logins) != 1 || !logins["default"].Default || logins["default"].SignedIn {
		t.Fatalf("a new hub's logins: %+v, want the default alone, signed out", logins)
	}
	hub.mustJSON("POST", "/api/claude-logins", map[string]string{"name": "ada"}, nil)
	hub.wantRefusal("POST", "/api/claude-logins", map[string]string{"name": "ada"}, 409, "claude_login")
	hub.wantRefusal("POST", "/api/claude-logins", map[string]string{"name": "default"}, 409, "claude_login")
	hub.wantRefusal("POST", "/api/claude-logins", map[string]string{"name": "Not A Name"}, 400, "claude_login")
	hub.wantRefusal("POST", "/api/claude-logins/nobody/sign-in", nil, 404, "claude_login")

	hub.signInClaudeLogin("default", "")
	hub.signInClaudeLogin("ada", "ada")
	logins := hub.claudeLogins()
	if got := logins["default"]; !got.SignedIn || got.Email != "fixture@example.test" {
		t.Errorf("the default after its sign-in: %+v, want signed in as fixture@example.test", got)
	}
	if got := logins["ada"]; !got.SignedIn || got.Email != "ada@example.test" {
		t.Errorf("ada after her sign-in: %+v, want signed in as ada@example.test", got)
	}
	if entries, err := os.ReadDir(filepath.Join(dataDir, "cloud-sign-in")); err != nil || len(entries) != 0 {
		t.Errorf("the sign-ins left %v in their directory (%v), want nothing", entries, err)
	}

	hub.createLoop("orbit", nil)
	resp, body := hub.do("DELETE", "/api/claude-logins/default", nil)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "orbit") {
		t.Fatalf("removing the default with orbit on it = %d %s, want 409 naming orbit", resp.StatusCode, body)
	}

	// The default's token is in its last hour and ada's has hours left: the
	// refresh, which looks at once when the hub starts, rotates the
	// default's alone.
	hub.stop()
	db := openStoreAt(t, dataDir)
	ctx := context.Background()
	defaultLogin, err := db.ClaudeLogins().Get(ctx, store.DefaultClaudeLogin)
	if err != nil {
		t.Fatal(err)
	}
	account := defaultLogin.Account
	account.ExpiresAt = time.Now().Add(10 * time.Minute).UnixMilli()
	if err := db.ClaudeLogins().SetAccount(ctx, store.DefaultClaudeLogin, account); err != nil {
		t.Fatal(err)
	}
	db.Close()
	hub = startServerArgs(t, dataDir, "--runtime", "bare")
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(hub.log(), `"Claude login refreshed" login=default`) {
		if time.Now().After(deadline) {
			t.Fatalf("the default was not refreshed; the hub's log:\n%s", hub.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Contains(hub.log(), `"Claude login refreshed" login=ada`) {
		t.Error("ada's login was refreshed with hours left")
	}

	resp, body = hub.do("DELETE", "/api/claude-logins/ada", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("removing ada = %d %s, want 204", resp.StatusCode, body)
	}
	if _, ok := hub.claudeLogins()["ada"]; ok {
		t.Error("ada is still listed after her removal")
	}
	revoked, _ := os.ReadFile(filepath.Join(hub.fkState, "revoked"))
	if !strings.Contains(string(revoked), "sk-ant-ort01-fixture-cloud-refresh-ada") {
		t.Errorf("removing ada revoked %q, want her refresh token", revoked)
	}
	hub.stop()
	db = openStoreAt(t, dataDir)
	defer db.Close()
	if login, err := db.ClaudeLogins().Get(ctx, store.DefaultClaudeLogin); err != nil || !strings.HasSuffix(login.Account.Token, "-r") {
		t.Errorf("the default's token after the refresh ends %q (%v), want rotated", login.Account.Token[max(0, len(login.Account.Token)-4):], err)
	}
}

// Any user may add a Claude login of their own and becomes its owner. Its
// owner or an admin may sign it out or remove it; another member may not
// (ADR-0060).
func TestAClaudeLoginIsItsOwnersAndTheAdmins(t *testing.T) {
	t.Parallel()
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare")
	mia := hub.signedInAs("mia", "member")
	ned := hub.signedInAs("ned", "member")
	admin := hub.signedInAs("ada", "admin")

	if resp, body := hub.asCookie("POST", "/api/claude-logins", mia, map[string]string{"name": "mia"}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("mia adding her login = %d %s, want 201", resp.StatusCode, body)
	}
	if got := hub.claudeLogins()["mia"]; got.Owner != "mia" {
		t.Errorf("mia's login: %+v, want her as its owner", got)
	}
	for _, call := range []struct{ method, path string }{
		{"POST", "/api/claude-logins/mia/sign-in"},
		{"POST", "/api/claude-logins/mia/sign-out"},
		{"DELETE", "/api/claude-logins/mia"},
		{"POST", "/api/claude-logins/default/sign-in"},
	} {
		resp, body := hub.asCookie(call.method, call.path, ned, nil)
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), `"forbidden_role"`) {
			t.Errorf("ned %s %s = %d %s, want 403 forbidden_role", call.method, call.path, resp.StatusCode, body)
		}
	}
	if resp, body := hub.asCookie("POST", "/api/claude-logins/mia/sign-out", mia, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("mia signing her login out = %d %s, want 200", resp.StatusCode, body)
	}
	if resp, body := hub.asCookie("DELETE", "/api/claude-logins/mia", admin, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("an admin removing mia's login = %d %s, want 204", resp.StatusCode, body)
	}
}

// A member sees the address only on the logins they own; the name, owner
// and signed-in state of every login stay theirs to see. Removing a user
// signs out the logins they own, revoked at the source, and keeps them
// (Enes, 2026-10-27, #1148).
func TestAUsersLoginsAreTheirsToSeeAndGoWithThem(t *testing.T) {
	t.Parallel()
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare")
	mia := hub.signedInAs("mia", "member")
	ned := hub.signedInAs("ned", "member")
	if resp, body := hub.asCookie("POST", "/api/claude-logins", mia, map[string]string{"name": "mia"}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("mia adding her login = %d %s, want 201", resp.StatusCode, body)
	}
	hub.signInClaudeLogin("default", "")
	hub.signInClaudeLogin("mia", "mia")

	seenBy := func(cookie string) map[string]claudeLoginJSON {
		t.Helper()
		resp, body := hub.asCookie("GET", "/api/claude-logins", cookie, nil)
		var view struct {
			Logins []claudeLoginJSON `json:"logins"`
		}
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &view) != nil {
			t.Fatalf("listing the logins = %d %s", resp.StatusCode, body)
		}
		byName := map[string]claudeLoginJSON{}
		for _, login := range view.Logins {
			byName[login.Name] = login
		}
		return byName
	}
	if got := seenBy(mia)["mia"]; got.Email != "mia@example.test" {
		t.Errorf("mia's own login as she sees it: %+v, want her address", got)
	}
	nedSees := seenBy(ned)
	if got := nedSees["mia"]; got.Email != "" || got.Owner != "mia" || !got.SignedIn {
		t.Errorf("mia's login as ned sees it: %+v, want her name and state but no address", got)
	}
	if got := nedSees["default"]; got.Email != "" || !got.SignedIn {
		t.Errorf("the default as ned sees it: %+v, want its state but no address", got)
	}

	if resp, body := hub.do("DELETE", "/api/users/mia", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("removing mia = %d %s, want 204", resp.StatusCode, body)
	}
	if got, ok := hub.claudeLogins()["mia"]; !ok || got.SignedIn {
		t.Errorf("mia's login after her removal: %+v (listed %v), want it kept, signed out", got, ok)
	}
	revoked, _ := os.ReadFile(filepath.Join(hub.fkState, "revoked"))
	if !strings.Contains(string(revoked), "sk-ant-ort01-fixture-cloud-refresh-mia") {
		t.Errorf("removing mia revoked %q, want her login's refresh token", revoked)
	}
	if got := hub.claudeLogins()["default"]; !got.SignedIn {
		t.Errorf("the default after mia's removal: %+v, want it still signed in", got)
	}
}

// adaLoginToken is the synthetic access token of the login named ada.
const adaLoginToken = "sk-ant-oat01-fixture-login-ada"

// lastAuthorization is the Authorization header the stand-in last saw.
func lastAuthorization(fake *anthropicFake) string {
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	if len(fake.Authorizations) == 0 {
		return ""
	}
	return fake.Authorizations[len(fake.Authorizations)-1]
}

// A contained loop runs on the Claude login it names, and one that names
// none on the default (ADR-0060). The Claude proxy picks the token
// by the calling loop's login at each request, so a move reaches the next
// one. A bare loop names no login, nor does a loop name one the hub
// doesn't hold, and a login a loop runs on can't be removed. A login
// signed out holds its loops: the proxy never falls back to the default.
func TestEachContainedLoopRunsOnItsOwnClaudeLogin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := newAnthropicFake(t, cloudFixtureToken)
	dataDir := t.TempDir()
	hub := startServerArgs(t, dataDir, "--runtime", "bare")
	for _, name := range []string{"aster", "bloom", "cosmos"} {
		hub.createLoop(name, nil)
	}
	hub.stop()
	// two contained loops, which this hub can't run but whose model
	// requests the proxy serves all the same, and one bare
	db := openStoreAt(t, dataDir)
	putDefaultLogin(t, db, store.CloudAccount{Token: cloudFixtureToken, ExpiresAt: time.Now().Add(8 * time.Hour).UnixMilli()})
	if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: "ada", Account: store.CloudAccount{Token: adaLoginToken, ExpiresAt: time.Now().Add(8 * time.Hour).UnixMilli()}, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE loops SET runtime = 'docker' WHERE name IN ('aster', 'bloom')`,
		`UPDATE loops SET claude_login = 'ada' WHERE name = 'bloom'`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	raw.Close()
	hub = startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-api-base", fake.URL)
	aster, bloom := hubMCPToken(t, hub, "aster"), hubMCPToken(t, hub, "bloom")

	if got := postModel(t, hub, "POST", "/claude/v1/messages", aster); got != http.StatusOK || lastAuthorization(fake) != "Bearer "+cloudFixtureToken {
		t.Errorf("aster's model request: %d on %.20q…, want 200 on the default's token", got, lastAuthorization(fake))
	}
	// the stand-in admits the default's token alone, so ada's is refused there
	if got := postModel(t, hub, "POST", "/claude/v1/messages", bloom); got != http.StatusUnauthorized || lastAuthorization(fake) != "Bearer "+adaLoginToken {
		t.Errorf("bloom's model request: %d on %.20q…, want it sent on ada's token", got, lastAuthorization(fake))
	}
	if got := hub.loop("bloom").ClaudeLogin; got != "ada" {
		t.Errorf("bloom's view names %q, want ada", got)
	}

	var moved loopView
	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"claude_login": "ada"}, &moved)
	if moved.ClaudeLogin != "ada" {
		t.Errorf("aster after its move names %q, want ada", moved.ClaudeLogin)
	}
	if postModel(t, hub, "POST", "/claude/v1/messages", aster); lastAuthorization(fake) != "Bearer "+adaLoginToken {
		t.Errorf("aster's request after the move went on %.20q…, want ada's token", lastAuthorization(fake))
	}
	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"claude_login": "default"}, &moved)
	if moved.ClaudeLogin != "" {
		t.Errorf("aster moved back names %q, want none, the default", moved.ClaudeLogin)
	}

	hub.wantRefusal("PATCH", "/api/loops/aster", map[string]any{"claude_login": "nobody"}, 400, "claude_login")
	hub.wantRefusal("PATCH", "/api/loops/cosmos", map[string]any{"claude_login": "ada"}, 409, "claude_login")
	hub.wantRefusal("POST", "/api/loops", map[string]any{"name": "dahlia", "mission": "m", "claude_login": "ada"}, 409, "claude_login")
	resp, body := hub.do("DELETE", "/api/claude-logins/ada", nil)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "bloom") || strings.Contains(string(body), "aster") {
		t.Errorf("removing ada with bloom on it = %d %s, want 409 naming bloom alone", resp.StatusCode, body)
	}

	hub.mustJSON("POST", "/api/claude-logins/ada/sign-out", nil, nil)
	fake.Mu.Lock()
	seen := len(fake.Authorizations)
	fake.Mu.Unlock()
	if got := postModel(t, hub, "POST", "/claude/v1/messages", bloom); got != http.StatusUnauthorized {
		t.Errorf("bloom's request with ada signed out answered %d, want 401", got)
	}
	fake.Mu.Lock()
	reached := len(fake.Authorizations) - seen
	fake.Mu.Unlock()
	if reached != 0 {
		t.Error("bloom's request with ada signed out reached Anthropic, on the default's token or another")
	}
}
