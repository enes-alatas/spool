//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// The hub refreshes the Claude account's token in its last hour with the
// CLI that signed it in, at a moment no contained loop is awake, and stores
// the rotated pair: the calls it makes after carry the new token, which the
// stand-in API now admits alone, and nothing unsealed is left behind
// (ADR-0058).
func TestTheHubRefreshesTheClaudeAccountInItsLastHour(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "nimbus")
	s.stop()
	storeCloudAccount(t, s.dataDir, store.CloudAccount{
		Token:        cloudFixtureToken,
		RefreshToken: "sk-ant-ort01-fixture-cloud-refresh",
		ExpiresAt:    time.Now().Add(30 * time.Minute).UnixMilli(),
		OrgUUID:      "00000000-0000-4000-8000-000000000001",
		Scopes:       []string{"user:inference", "user:sessions:claude_code"},
	})
	// fakeclaude's refresh appends "-r" to both tokens
	admitOnly(t, s, cloudFixtureToken+"-r")
	s = startServerArgs(t, s.dataDir, "--runtime", "bare")

	deadline := time.Now().Add(20 * time.Second)
	for {
		var view cloudSettings
		s.mustJSON("POST", "/api/settings/cloud/account/check", nil, &view)
		if view.Account.Check != nil && view.Account.Check.State == "ok" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the account still checks as %+v, want ok on the refreshed token\n%s", view.Account.Check, s.log())
		}
		time.Sleep(200 * time.Millisecond)
	}
	// the log line follows the store by a moment
	for !strings.Contains(s.log(), "Claude login refreshed") {
		if time.Now().After(deadline) {
			t.Fatalf("the hub logged no refresh\n%s", s.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
	// the refresh's config directory goes with it, tokens and all
	left, err := filepath.Glob(filepath.Join(s.dataDir, "cloud-sign-in", "refresh-*"))
	if err != nil || len(left) != 0 {
		t.Errorf("the refresh left %v behind (%v)", left, err)
	}
	wantQuietCLIRuns(t, s)
	// the rotated tokens are secrets like the first: never in the log
	if strings.Contains(s.log(), cloudFixtureToken+"-r") || strings.Contains(s.log(), "sk-ant-ort01-fixture-cloud-refresh-r") {
		t.Error("the log carries a rotated token")
	}
}

// A refresh the account's server refuses keeps the token the hub has, and
// says so in the log; the hub tries again rather than holding the loops on
// every try (ADR-0058).
func TestARefusedLoginRefreshKeepsTheToken(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "stratus")
	s.stop()
	storeCloudAccount(t, s.dataDir, store.CloudAccount{
		Token: cloudFixtureToken,
		// fakeclaude refuses to refresh a refresh token marked revoked
		RefreshToken: "sk-ant-ort01-fixture-revoked",
		ExpiresAt:    time.Now().Add(30 * time.Minute).UnixMilli(),
		OrgUUID:      "00000000-0000-4000-8000-000000000001",
	})
	s = startServerArgs(t, s.dataDir, "--runtime", "bare")

	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(s.log(), "Claude login refresh failed") {
		if time.Now().After(deadline) {
			t.Fatalf("no failed refresh logged\n%s", s.log())
		}
		time.Sleep(200 * time.Millisecond)
	}
	// the stand-in admits the old token alone, and the hub still holds it
	var view cloudSettings
	s.mustJSON("POST", "/api/settings/cloud/account/check", nil, &view)
	if view.Account.Check == nil || view.Account.Check.State != "ok" {
		t.Fatalf("after a refused refresh the account checks as %+v, want ok on the token kept", view.Account.Check)
	}
	if left, _ := filepath.Glob(filepath.Join(s.dataDir, "cloud-sign-in", "refresh-*")); len(left) != 0 {
		t.Errorf("the refused refresh left %v behind", left)
	}
}

// Sign out revokes the Claude login at the source with the CLI's own
// logout, run in a config directory of the hub's that goes with it, and
// forgets the login. A logout that fails still forgets it, and the log
// says to revoke it at claude.ai (ADR-0058).
func TestSignOutRevokesTheClaudeLoginAtTheSource(t *testing.T) {
	t.Parallel()
	const refresh = "sk-ant-ort01-fixture-cloud-refresh"
	s := startCloudHub(t, "nimbostratus")
	s.stop()
	login := store.CloudAccount{
		Token:        cloudFixtureToken,
		RefreshToken: refresh,
		ExpiresAt:    time.Now().Add(8 * time.Hour).UnixMilli(),
		OrgUUID:      "00000000-0000-4000-8000-000000000001",
	}
	storeCloudAccount(t, s.dataDir, login)
	s = startServerArgs(t, s.dataDir, "--runtime", "bare")

	var view cloudSettings
	s.mustJSON("DELETE", "/api/settings/cloud/account", nil, &view)
	if view.Account.Set {
		t.Fatal("Sign out left the login signed in")
	}
	revoked, err := os.ReadFile(filepath.Join(s.fkState, "revoked"))
	if err != nil || strings.TrimSpace(string(revoked)) != refresh {
		t.Fatalf("the CLI revoked %q (%v), want the login's refresh token", revoked, err)
	}
	wantQuietCLIRuns(t, s)
	if !strings.Contains(s.log(), "Claude login signed out at the source") {
		t.Errorf("the hub logged no sign-out\n%s", s.log())
	}
	left, err := filepath.Glob(filepath.Join(s.dataDir, "cloud-sign-in", "sign-out-*"))
	if err != nil || len(left) != 0 {
		t.Errorf("the sign-out left %v behind (%v)", left, err)
	}

	// a logout that fails still signs the hub out
	s.stop()
	storeCloudAccount(t, s.dataDir, login)
	if err := os.WriteFile(filepath.Join(s.fkState, "logout-fails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s = startServerArgs(t, s.dataDir, "--runtime", "bare")
	s.mustJSON("DELETE", "/api/settings/cloud/account", nil, &view)
	if view.Account.Set {
		t.Fatal("a failed logout left the login signed in")
	}
	if !strings.Contains(s.log(), "Claude login not revoked at the source; revoke it at claude.ai") {
		t.Errorf("the hub didn't say the revoke failed\n%s", s.log())
	}
	if strings.Contains(s.log(), refresh) {
		t.Error("the log carries the refresh token")
	}
}
