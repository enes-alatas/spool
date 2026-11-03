package claude

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// A claude run in a config directory gets the hub's security first on its
// PATH, in a private directory of the run's own, and the directory pinned
// as the one its login is named for; nothing else in its env changes, and
// one inherited pin is dropped (#1304).
func TestTheKeychainEnvPutsTheHubsSecurityFirst(t *testing.T) {
	configDir := t.TempDir()
	env, err := KeychainEnv([]string{"HOME=/home/fixture", "PATH=/usr/bin:/bin", "CLAUDE_SECURESTORAGE_CONFIG_DIR=/elsewhere"}, configDir)
	if err != nil {
		t.Fatal(err)
	}
	shimDir := KeychainShimDir(configDir)
	want := []string{"HOME=/home/fixture", "PATH=" + shimDir + ":/usr/bin:/bin", "CLAUDE_SECURESTORAGE_CONFIG_DIR=" + configDir}
	if !slices.Equal(env, want) {
		t.Fatalf("KeychainEnv = %q, want %q", env, want)
	}
	if info, err := os.Stat(shimDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the security's directory: %v %v, want 0700", info, err)
	}
	shim := filepath.Join(shimDir, "security")
	if info, err := os.Stat(shim); err != nil || info.Mode().Perm() != 0o500 {
		t.Errorf("the security: %v %v, want 0500", info, err)
	}
	// it answers a write as errSecItemNotFound, and so a read and a delete
	var exit *exec.ExitError
	if err := exec.Command(shim, "add-generic-password", "-U", "-a", "fixture", "-s", "Claude Code-credentials-00000000", "-X", "7b7d").Run(); !errors.As(err, &exit) || exit.ExitCode() != 44 {
		t.Errorf("the hub's security exited %v, want 44", err)
	}

	// a directory made already isn't the run's own
	if _, err := KeychainEnv(nil, configDir); err == nil {
		t.Error("a second KeychainEnv over the same directory was taken, want it refused")
	}
	// a relative directory names nothing once the CLI runs inside it
	if _, err := KeychainEnv(nil, filepath.Join("data", "cloud-sign-in", "login-1")); err == nil {
		t.Error("KeychainEnv took a relative directory, want it refused")
	}
}

// The Keychain item is named as 2.1.316 names it: "Claude Code-credentials-"
// and the first 8 hex digits of the SHA-256 of the directory.
func TestTheKeychainItemIsNamedForItsDirectory(t *testing.T) {
	// sha256("/fixture/config") = 9e67da4d…
	if got := KeychainItem("/fixture/config"); got != "Claude Code-credentials-9e67da4d" {
		t.Errorf("KeychainItem = %q", got)
	}
}
