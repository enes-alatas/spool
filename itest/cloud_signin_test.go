//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signInCloudFromThePage signs the hub in to the fixture account the way
// Settings → Cloud does: the CLI's own login prints a link, and the code
// the page would show is handed back. It returns the settings after.
func signInCloudFromThePage(t *testing.T, s *server) cloudSettings {
	t.Helper()
	var started struct {
		Link string `json:"link"`
	}
	s.mustJSON("POST", "/api/settings/cloud/account/sign-in", nil, &started)
	if !strings.HasPrefix(started.Link, "https://") || !strings.Contains(started.Link, "oauth/authorize") {
		t.Fatalf("the sign-in's link = %q, want the CLI's authorize link", started.Link)
	}
	var view cloudSettings
	s.mustJSON("POST", "/api/settings/cloud/account/code", map[string]string{"code": "fixture-code#fixture-state"}, &view)
	return view
}

// Settings → Cloud signs in to the account cloud loops spend with the
// Claude Code CLI's own login, run in a config directory of the hub's that
// is gone once the sign-in ends (ADR-0056). The sign-in is checked with
// one login check (ADR-0059). A refused code, or a code with no sign-in waiting,
// says so. While no account is signed in, a cloud loop waits, and a
// sign-in wakes it with what waited. Remove forgets the account.
func TestSettingsCloudSignsInWithTheCLIsLogin(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "cirrus")

	s.mustJSON("DELETE", "/api/settings/cloud/account", nil, nil)
	var view cloudSettings
	s.mustJSON("GET", "/api/settings/cloud", nil, &view)
	if view.Account.Set || view.Account.Check == nil || view.Account.Check.Stage != "no_account" {
		t.Fatalf("after Remove: %+v %+v, want no account, checked as none", view.Account, view.Account.Check)
	}

	before := len(s.turns("cirrus"))
	s.message("cirrus", "held words")
	time.Sleep(2 * time.Second)
	if after := len(s.turns("cirrus")); after != before {
		t.Fatalf("a cloud loop with no account ran %d turns, want none", after-before)
	}
	// held, not tried: a wake would have failed for want of the account
	if strings.Contains(s.log(), "no Claude account is signed in") {
		t.Fatal("the cloud loop tried to wake with no account, want it held")
	}

	s.wantRefusal("POST", "/api/settings/cloud/account/code", map[string]string{"code": "fixture-code"}, 409, "cloud_sign_in")
	s.mustJSON("POST", "/api/settings/cloud/account/sign-in", nil, nil)
	// a code is one word of at most 1 KB: a second line never reaches the
	// CLI as another answer, and a sign-in refused for its shape still waits
	s.wantRefusal("POST", "/api/settings/cloud/account/code", map[string]string{"code": "fixture-code\nmore"}, 400, "cloud_sign_in")
	s.wantRefusal("POST", "/api/settings/cloud/account/code", map[string]string{"code": "fixture-code more"}, 400, "cloud_sign_in")
	s.wantRefusal("POST", "/api/settings/cloud/account/code", map[string]string{"code": "fixture-code" + strings.Repeat("x", 1<<10)}, 400, "cloud_sign_in")
	s.wantRefusal("POST", "/api/settings/cloud/account/code", map[string]string{"code": "not-the-code"}, 400, "cloud_sign_in")
	s.wantRefusal("POST", "/api/settings/cloud/account/code", map[string]string{"code": "fixture-code"}, 409, "cloud_sign_in")

	checks := loginChecks(t, s)
	view = signInCloudFromThePage(t, s)
	if !view.Account.Set || view.Account.Check == nil || view.Account.Check.State != "ok" {
		t.Fatalf("after the sign-in: %+v %+v, want an account checked ok", view.Account, view.Account.Check)
	}
	checked := loginChecks(t, s) - checks
	if checked != 1 {
		t.Errorf("the sign-in made %d account checks, want 1", checked)
	}
	if entries, err := os.ReadDir(filepath.Join(s.dataDir, "cloud-sign-in")); err != nil || len(entries) != 0 {
		t.Errorf("the sign-in left %v in its directory (%v), want nothing: the tokens are kept sealed alone", entries, err)
	}
	s.waitTurn("cirrus", 20*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "held words") })
	wantQuietCLIRuns(t, s)

	if strings.Contains(s.log(), "fixture-cloud-account") || strings.Contains(s.log(), "fixture-cloud-refresh") {
		t.Error("the hub's log quotes the account's tokens")
	}
}

// A hub stopped mid-sign-in leaves the CLI's tokens unsealed in its
// directory; the next hub removes them as it starts, before anything can
// copy the data directory without its key (ADR-0046).
func TestAStartingHubRemovesAnUnfinishedCloudSignIn(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	left := filepath.Join(dataDir, "cloud-sign-in", "login-1")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(left, ".credentials.json"), []byte(`{"claudeAiOauth":{"refreshToken":"fixture-cloud-refresh"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	startServer(t, dataDir)
	if _, err := os.Stat(filepath.Join(dataDir, "cloud-sign-in")); !os.IsNotExist(err) {
		t.Fatalf("the unfinished sign-in's directory after a start: %v, want it gone", err)
	}
}

// A Claude login supersedes the setup-token (ADR-0058). A sign-in clears
// a stored setup-token, and a setup-token is refused while a login is
// signed in, so a Sign out never falls back to one. Throughout, Settings
// reads the hub as having a Claude token while it has either, and says the
// hub finds the CLI it signs in with.
func TestAClaudeLoginSupersedesTheSetupToken(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "altostratus")
	const setupToken = "sk-ant-oat01-itesttokenABCDEFGHIJKLMNOP0123456789"

	var view cloudSettings
	s.mustJSON("GET", "/api/settings/cloud", nil, &view)
	if !view.Account.Set || !view.Account.CLI {
		t.Fatalf("the account = %+v, want a login signed in with the CLI found", view.Account)
	}
	if !s.settings().ClaudeTokenSet {
		t.Fatal("a hub with a Claude login reads as having no Claude token")
	}
	if resp, body := s.do("PUT", "/api/settings", map[string]any{"claude_oauth_token": setupToken}); resp.StatusCode != 409 {
		t.Fatalf("a setup-token under a Claude login answered %d, want 409 (%s)", resp.StatusCode, body)
	}

	s.mustJSON("DELETE", "/api/settings/cloud/account", nil, nil)
	if s.settings().ClaudeTokenSet {
		t.Fatal("a hub signed out with no setup-token reads as having a Claude token")
	}
	s.mustJSON("PUT", "/api/settings", map[string]any{"claude_oauth_token": setupToken}, nil)
	if !s.settings().ClaudeTokenSet {
		t.Fatal("a setup-token with no login signed in was not stored")
	}

	signInCloudFromThePage(t, s)
	s.mustJSON("DELETE", "/api/settings/cloud/account", nil, nil)
	if s.settings().ClaudeTokenSet {
		t.Fatal("the setup-token outlived the sign-in that superseded it")
	}
}

// wantQuietCLIRuns fails a test whose hub ran the claude CLI for its login
// without CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: fakeclaude notes each
// auth run that would have posted the CLI's event logging (#935).
func wantQuietCLIRuns(t *testing.T, s *server) {
	t.Helper()
	logged, err := os.ReadFile(filepath.Join(s.fkState, "event-logging"))
	if err == nil {
		t.Errorf("the hub ran the claude CLI with its telemetry on: %q", strings.Split(strings.TrimSpace(string(logged)), "\n"))
	} else if !os.IsNotExist(err) {
		t.Error(err)
	}
}

// loginChecks counts the login checks fakeclaude has run for s.
func loginChecks(t *testing.T, s *server) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.fkState, "login-checks"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "\n")
}

// admitOnly has fakeclaude take token alone as the Claude login, as the API
// takes a refreshed account's new token and no longer its old one.
func admitOnly(t *testing.T, s *server, token string) {
	t.Helper()
	if err := os.MkdirAll(s.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.fkState, "admitted-token"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
}
