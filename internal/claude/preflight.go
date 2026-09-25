package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TestedVersion is the CLI version Spool's stream-json handling was verified
// against. Other versions are allowed but logged.
const TestedVersion = "2.1.281"

// Preflight checks the claude binary exists and returns its version string.
// It is an auxiliary run (ADR-0033): the environment is built from nothing,
// so no credential the hub was started with reaches it.
func Preflight(ctx context.Context, bin string) (string, error) {
	if bin == "" {
		bin = "claude"
	}
	home, err := os.MkdirTemp("", "spool-aux-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(home) }()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Dir = home
	cmd.Env = AuxEnv(os.Getenv("PATH"), home, "")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("claude binary %q not runnable: %w (output: %s)", bin, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
