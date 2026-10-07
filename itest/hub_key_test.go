//go:build integration

package itest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// The synthetic secrets the hub key test stores.
const (
	sealedMarker     = "marker-synthetic-sealed-secret"
	sealedSetupToken = "sk-ant-oat01-synthetic-sealed"
)

// TestTheHubSealsItsSecretsUnderItsKey pins ADR-0046 (#623) end to end: the
// secrets a hub stores are not in its database's files, a restarted hub
// opens them with its key, a loop is found by its hub MCP token after the
// restart, and a hub whose key is gone refuses to start rather than mint
// another.
func TestTheHubSealsItsSecretsUnderItsKey(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("keeper", nil)
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "script", "kind": "env-var", "config": map[string]any{"env": "FAKECLAUDE_SCRIPT"},
		"secret": "!env MARKER",
	}, nil)
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "marker", "kind": "env-var", "config": map[string]any{"env": "MARKER"}, "secret": sealedMarker,
	}, nil)
	for _, name := range []string{"script", "marker"} {
		s.mustJSON("PUT", "/api/loops/keeper/connections/"+name, nil, nil)
	}
	s.mustJSON("PUT", "/api/settings", map[string]any{"claude_oauth_token": sealedSetupToken}, nil)
	token := hubMCPToken(t, s, "keeper")
	s.stop()

	for _, file := range []string{"spool.db", "spool.db-wal"} {
		data, err := os.ReadFile(filepath.Join(dataDir, file))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, secret := range []string{sealedMarker, sealedSetupToken, token} {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s holds %s… in the clear", file, secret[:8])
			}
		}
	}

	// the restarted hub opens what it sealed: the secret reaches the loop,
	// whose reply the redactor masks because it knows the opened value, and
	// the loop's token still finds it
	s = startServer(t, dataDir)
	s.message("keeper", "after the restart")
	s.waitTurn("keeper", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=<redacted:MARKER>" })
	mcpSession(t, s, token)
	s.stop()

	keyPath := filepath.Join(dataDir, sqlite.KeyFile)
	if err := os.Rename(keyPath, keyPath+".away"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(repoRoot(t), "bin", "spool"),
		"--runtime", "bare", "--data-dir", dataDir, "--claude-bin", filepath.Join(repoRoot(t), "bin", "fakeclaude"),
		"--listen", "127.0.0.1:0", "--mcp-listen", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("a hub without its key did not exit 1: err=%v, output:\n%s", err, out)
	}
	for _, want := range []string{sqlite.KeyFile, "restore"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the refusal never mentions %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("a key was minted in place of the lost one: %v", err)
	}
}
