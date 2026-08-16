package claude

import (
	"fmt"
	"os/exec"
	"strings"
)

// TestedVersion is the CLI version Spool's stream-json handling was verified
// against. Other versions are allowed but logged.
const TestedVersion = "2.1.233"

// Preflight checks the claude binary exists and returns its version string.
func Preflight(bin string) (string, error) {
	if bin == "" {
		bin = "claude"
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("claude binary %q not runnable: %w (output: %s)", bin, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
