//go:build integration

package itest

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// bobLoginToken is the synthetic access token of the login named bob.
const bobLoginToken = "sk-ant-oat01-fixture-login-bob"

// A contained loop that names no Claude login runs on its team's, and one
// that names its own keeps it; a loop in no team, a bare loop and a cloud
// loop resolve as before. The Claude proxy picks the token at each request,
// so a team's new login reaches its next one. A team can't name a login the
// hub doesn't hold, and a login a team names can't be deleted (ADR-0066).
func TestATeamsContainedLoopsRunOnItsClaudeLogin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := newAnthropicFake(t, cloudFixtureToken)
	dataDir := t.TempDir()
	hub := startServerArgs(t, dataDir, "--runtime", "bare")
	for _, name := range []string{"aster", "bloom", "cosmos", "dahlia", "elm"} {
		hub.createLoop(name, nil)
	}
	hub.stop()
	// three contained loops and a cloud one, which this hub can't run but
	// whose model requests the proxy serves all the same, and one bare
	db := openStoreAt(t, dataDir)
	putDefaultLogin(t, db, store.CloudAccount{Token: cloudFixtureToken, ExpiresAt: time.Now().Add(8 * time.Hour).UnixMilli()})
	for name, token := range map[string]string{"ada": adaLoginToken, "bob": bobLoginToken} {
		account := store.CloudAccount{Token: token, ExpiresAt: time.Now().Add(8 * time.Hour).UnixMilli()}
		if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: name, Account: account, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE loops SET runtime = 'docker' WHERE name IN ('aster', 'bloom', 'dahlia')`,
		`UPDATE loops SET claude_login = 'bob' WHERE name = 'bloom'`,
		`UPDATE loops SET runtime = 'cloud' WHERE name = 'elm'`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	raw.Close()
	hub = startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-api-base", fake.URL)
	aster, bloom, dahlia := hubMCPToken(t, hub, "aster"), hubMCPToken(t, hub, "bloom"), hubMCPToken(t, hub, "dahlia")

	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "ghost", "claude_login": "nobody"}, 400, "claude_login")
	var red teamJSON
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "red", "claude_login": "ada"}, &red)
	if red.ClaudeLogin != "ada" {
		t.Fatalf("red = %+v, want it on ada", red)
	}
	for _, name := range []string{"aster", "bloom", "cosmos", "elm"} {
		hub.mustJSON("PATCH", "/api/loops/"+name, map[string]any{"team": "red"}, nil)
	}

	wantLogin := func(name, login, from string) {
		t.Helper()
		if view := hub.loop(name); view.EffectiveLogin != login || view.LoginFrom != from {
			t.Errorf("%s runs on %q from %q, want %q from %q", name, view.EffectiveLogin, view.LoginFrom, login, from)
		}
	}
	wantToken := func(name, mcpToken, token string) {
		t.Helper()
		if postModel(t, hub, "POST", "/claude/v1/messages", mcpToken); lastAuthorization(fake) != "Bearer "+token {
			t.Errorf("%s's model request went on %.20q…, want %.20q…", name, lastAuthorization(fake), token)
		}
	}
	wantLogin("aster", "ada", "team")
	wantToken("aster", aster, adaLoginToken)
	wantLogin("bloom", "bob", "loop")
	wantToken("bloom", bloom, bobLoginToken)
	wantLogin("cosmos", "", "host")
	wantLogin("dahlia", "default", "hub")
	wantLogin("elm", "default", "hub") // a cloud loop never takes its team's
	wantToken("dahlia", dahlia, cloudFixtureToken)

	hub.wantRefusal("PATCH", "/api/teams/red", map[string]any{"claude_login": "nobody"}, 400, "claude_login")
	hub.mustJSON("PATCH", "/api/teams/red", map[string]any{"claude_login": "bob"}, &red)
	if red.ClaudeLogin != "bob" {
		t.Fatalf("red after its move = %+v, want it on bob", red)
	}
	wantLogin("aster", "bob", "team")
	wantToken("aster", aster, bobLoginToken)
	resp, body := hub.do("DELETE", "/api/claude-logins/bob", nil)
	if resp.StatusCode != http.StatusConflict || errorCode(body) != "claude_login" || !strings.Contains(string(body), "red") {
		t.Errorf("removing bob while red names it = %d %s, want 409 naming red", resp.StatusCode, body)
	}
	// ada now has no team and no loop on it
	hub.wantRefusal("DELETE", "/api/claude-logins/ada", nil, 204, "")

	// leaving the team leaves its login
	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"team": ""}, nil)
	wantLogin("aster", "default", "hub")
	wantToken("aster", aster, cloudFixtureToken)
	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"team": "red"}, nil)
	hub.mustJSON("PATCH", "/api/teams/red", map[string]any{"claude_login": "default"}, &red)
	if red.ClaudeLogin != "" {
		t.Errorf("red put back on the default names %q, want none", red.ClaudeLogin)
	}
	wantLogin("aster", "default", "hub")
	wantToken("aster", aster, cloudFixtureToken)
}
