package loop

import (
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/claude"
)

// The excerpt starts on a whole line: the store redacts after the cut, so
// a secret the window splits must not show its tail (#1096).
func TestExitReasonNeverSplitsALine(t *testing.T) {
	secret := "synthetic-secret-" + strings.Repeat("x", 40)
	stderr := "warming up\n" + "remote said " + secret + "\n" + strings.Repeat("y", 270) + "\n"
	got := exitReason(claude.ExitInfo{Code: 1, Stderr: stderr}, false)
	if strings.Contains(got, "xxxx") {
		t.Errorf("the reason carries part of a line the window cut: %q", got)
	}
	if !strings.HasSuffix(got, strings.Repeat("y", 270)) {
		t.Errorf("the reason lost the whole last line: %q", got)
	}

	long := exitReason(claude.ExitInfo{Code: 1, Stderr: strings.Repeat("z", 400)}, false)
	if long != "claude exited (code 1) before the turn ended" {
		t.Errorf("a single line longer than the window shows %q, want none of it", long)
	}

	short := exitReason(claude.ExitInfo{Code: 2, Stderr: "boom\n"}, false)
	if short != "claude exited (code 2) before the turn ended: boom" {
		t.Errorf("a short stderr shows %q, want it whole", short)
	}
}
