package docker

import (
	"errors"
	"testing"
)

// Docker's stderr is docker's words, and a power refusal carries it to the
// room: it draws no copy chip (#959).
func TestCLIErrorQuotesStderrWithoutMarks(t *testing.T) {
	err := &cliError{subcommand: "run", err: errors.New("exit status 125"), stderr: "unknown flag `--x`"}
	if got, want := err.Error(), "docker run: exit status 125 (unknown flag '--x')"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
