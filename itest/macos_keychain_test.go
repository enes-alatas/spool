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

// asOnAMac makes the hub's fakeclaude store a sign-in as 2.1.316 does on
// macOS: in the login Keychain, through the security it finds on PATH or
// else the system's, deleting the file. The system's security here is a
// stand-in that writes down it was called and takes the write. It returns
// that record's path.
func asOnAMac(t *testing.T, hub *server) string {
	t.Helper()
	called := filepath.Join(hub.dataDir, "system-security-called")
	system := filepath.Join(hub.dataDir, "system-security")
	if err := os.WriteFile(system, []byte("#!/bin/sh\necho \"$1\" >> '"+called+"'\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hub.fkState, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hub.fkState, "keychain"), []byte(system), 0o600); err != nil {
		t.Fatal(err)
	}
	return called
}

// leftTheKeychainAlone fails the test if the system's security was called
// or the fake's Keychain holds an item.
func leftTheKeychainAlone(t *testing.T, hub *server, called string) {
	t.Helper()
	if calls, err := os.ReadFile(called); err == nil {
		t.Errorf("the CLI reached the system's security: %q", calls)
	}
	if items, err := os.ReadFile(filepath.Join(hub.fkState, "keychain-items")); err == nil {
		t.Errorf("the login went to the Keychain as %q", items)
	}
}

// On macOS the CLI keeps a login in the operator's login Keychain, through
// the security tool it calls by name. The hub's claude runs find a security
// of the hub's first on PATH, which answers that it has no item, so the
// CLI falls back to the file in the hub's config directory: a sign-in on
// the page ends with the login held, and the operator's Keychain is never
// touched (#1304).
func TestASignInOnAMacKeepsTheLoginOutOfTheKeychain(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	hub.signOutCloudAccount()
	called := asOnAMac(t, hub)

	view := signInCloudFromThePage(t, hub)
	if !view.Account.Set {
		t.Fatalf("after signing in on a Mac: %+v, want the login held\n%s", view.Account, hub.log())
	}
	leftTheKeychainAlone(t, hub, called)
}

// A refresh on macOS writes the rotated login back where the CLI keeps it,
// which past the hub's security is the file the hub reads it from, so the
// hub stores the rotated pair and the Keychain is left alone (#1304).
func TestARefreshOnAMacKeepsTheLoginOutOfTheKeychain(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "nimbus")
	hub.stop()
	storeCloudAccount(t, hub.dataDir, store.CloudAccount{
		Token:        cloudFixtureToken,
		RefreshToken: "sk-ant-ort01-fixture-cloud-refresh",
		ExpiresAt:    time.Now().Add(30 * time.Minute).UnixMilli(),
		OrgUUID:      "00000000-0000-4000-8000-000000000001",
		Scopes:       []string{"user:inference", "user:sessions:claude_code"},
	})
	called := asOnAMac(t, hub)
	// fakeclaude's refresh appends "-r" to both tokens
	admitOnly(t, hub, cloudFixtureToken+"-r")
	hub = startServerArgs(t, hub.dataDir, "--runtime", "bare")

	within(t, 20*time.Second, "the refreshed login checking ok", func() bool {
		view := hub.checkCloudAccount()
		return view.Account.Check != nil && view.Account.Check.State == "ok"
	})
	within(t, 5*time.Second, "the refresh logged", func() bool { return strings.Contains(hub.log(), "Claude login refreshed") })
	leftTheKeychainAlone(t, hub, called)
}
