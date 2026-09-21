//go:build integration

package itest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Spool takes flags, not subcommands, and Go's flag package stops parsing at
// the first non-flag argument instead of complaining. `spool serve --data-dir
// X` therefore used to discard every flag after `serve` and run the default
// fleet — the operator's own — minting and printing that fleet's operator
// token to stdout (#247).
//
// This is tier 2 because the property is about a process: its exit status,
// its stderr, and the directory it did not touch. HOME is redirected so the
// "default fleet" the mistyped command would have reached is a temporary one
// this test can then prove is untouched.
func TestAMistypedSubcommandIsRefused(t *testing.T) {
	spoolBin := filepath.Join(repoRoot(t), "bin", "spool")
	if _, err := os.Stat(spoolBin); err != nil {
		t.Fatalf("%s missing — run via `make itest`", spoolBin)
	}

	cases := []struct {
		name string
		args []string
	}{
		{name: "a subcommand that never existed", args: []string{"serve", "--data-dir", "IGNORED"}},
		{name: "the flag spelled as a word", args: []string{"version"}},
		{name: "a bare positional", args: []string{"spool.db"}},
		{name: "a directory where --data-dir belongs", args: []string{"token", "IGNORED"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			args := make([]string, len(tc.args))
			for i, a := range tc.args {
				if a == "IGNORED" {
					a = t.TempDir()
				}
				args[i] = a
			}

			cmd := exec.Command(spoolBin, args...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			out, err := cmd.CombinedOutput()

			// The credential assertions come first and never Fatalf: they
			// are what the issue is about, and against a binary without the
			// fix the exit status is the *least* interesting thing wrong.
			// Ordered the other way they would be skipped in exactly the
			// case they exist for.
			if _, err := os.Stat(filepath.Join(home, ".spool")); !os.IsNotExist(err) {
				t.Errorf("%s/.spool exists — the default fleet was opened", home)
			}
			if strings.Contains(string(out), "Operator token") {
				t.Errorf("a token was minted and printed by a refused invocation:\n%s", out)
			}
			if strings.Contains(string(out), "spool listening") {
				t.Errorf("the hub started on a command line it should have refused:\n%s", out)
			}

			// 2 is "your command line is wrong". Not compared against what a
			// running hub exits with: one that fails to bind exits 0 today,
			// which is its own bug (#253) and not this test's business.
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Errorf("spool %s did not exit non-zero: err=%v", strings.Join(args, " "), err)
			} else if exit.ExitCode() != 2 {
				t.Errorf("exit code = %d, want 2; output:\n%s", exit.ExitCode(), out)
			}
			if !strings.Contains(string(out), "unknown argument") {
				t.Errorf("stderr does not name the offending argument:\n%s", out)
			}
		})
	}
}

// The refusal must not cost a working invocation anything: `spool token` is a
// real subcommand and the only one.
func TestTokenSubcommandStillWorks(t *testing.T) {
	spoolBin := filepath.Join(repoRoot(t), "bin", "spool")
	dataDir := t.TempDir()

	cmd := exec.Command(spoolBin, "token", "--data-dir", dataDir)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("spool token failed: %v", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Error("spool token printed nothing")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "operator-token")); err != nil {
		t.Errorf("the token was not minted into --data-dir: %v", err)
	}
}
