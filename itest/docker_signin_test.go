//go:build integration

package itest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// A hub with no claude of its own signs in to the Claude account with the
// workstation image's, in a throwaway container behind the egress wall
// that sees only the sign-in's directory (#952). The page offers the
// sign-in, the code reaches the CLI and the account checks ok there, the token
// is refreshed the same way in its last hour, and Sign out runs there
// too. Nothing is left: no tokens in the data directory, no container.
func dockerSignInRunsInTheImage(t *testing.T) {
	dataDir := t.TempDir()
	noClaude := filepath.Join(t.TempDir(), "claude")
	s := startDockerServer(t, dataDir, "--claude-bin", noClaude)

	var view cloudSettings
	s.mustJSON("GET", "/api/settings/cloud", nil, &view)
	if !view.Account.CLI {
		t.Fatalf("a docker hub with no host claude reports no sign-in can run: %+v", view.Account)
	}
	view = signInCloudFromThePage(t, s)
	if !view.Account.Set || view.Account.Check == nil || view.Account.Check.State != "ok" {
		t.Fatalf("after the image's sign-in: %+v %+v, want an account checked ok", view.Account, view.Account.Check)
	}
	wantNothingLeft := func(after string) {
		t.Helper()
		if entries, err := os.ReadDir(filepath.Join(dataDir, "cloud-sign-in")); err == nil && len(entries) != 0 {
			t.Errorf("after %s the sign-in directory holds %v, want nothing", after, entries)
		}
		out, err := exec.Command("docker", "ps", "--all", "--quiet", "--filter", "label=spool.sign-in=1").Output()
		if err != nil {
			t.Fatal(err)
		}
		if left := strings.TrimSpace(string(out)); left != "" {
			t.Errorf("after %s the sign-in left containers %s", after, left)
		}
	}
	wantNothingLeft("the sign-in")

	// the refresh: the account in its last hour
	s.stop()
	storeCloudAccount(t, dataDir, store.CloudAccount{
		Token:        cloudFixtureToken,
		RefreshToken: "sk-ant-ort01-fixture-cloud-refresh",
		ExpiresAt:    time.Now().Add(30 * time.Minute).UnixMilli(),
		OrgUUID:      "00000000-0000-4000-8000-000000000001",
		Scopes:       []string{"user:inference", "user:sessions:claude_code"},
	})
	// started as startDockerServer does, less the setup-token, which a
	// signed-in hub refuses
	s = startServerOn(t, dataDir, "", "--runtime", "docker", "--workstation-image", workstationTestImage,
		"--egress-image", egressTestImage, "--workstation-health-sec", "2",
		"--claude-bin", noClaude)
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(s.log(), "Claude login refreshed") {
		if time.Now().After(deadline) {
			t.Fatalf("the image's claude didn't refresh the login\n%s", s.log())
		}
		time.Sleep(200 * time.Millisecond)
	}
	wantNothingLeft("the refresh")

	// The fake records a revoke in its state directory, which the
	// read-only container can't write, so its logout fails and the hub
	// forgets the login unrevoked: what is checked here is that the
	// logout ran in the image and left nothing.
	s.signOutCloudAccount()
	s.mustJSON("GET", "/api/settings/cloud", nil, &view)
	if view.Account.Set {
		t.Fatalf("after Sign out the account is still set: %+v", view.Account)
	}
	if !s.logged("claude auth logout") {
		t.Errorf("Sign out ran no logout\n%s", s.log())
	}
	wantNothingLeft("Sign out")
}
