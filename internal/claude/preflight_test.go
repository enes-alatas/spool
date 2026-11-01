package claude

import (
	"os"
	"strings"
	"testing"
)

// TestTestedVersionIsTheReleasePin: the CLI release images install is the
// one Spool was verified against, and the one fakeclaude says it is, so a
// bump of any of the three moves all of them (#1235).
func TestTestedVersionIsTheReleasePin(t *testing.T) {
	pin, err := os.ReadFile("../../docker/workstation/claude-code.version")
	if err != nil {
		t.Fatal(err)
	}
	if pinned := strings.TrimSpace(string(pin)); pinned != TestedVersion {
		t.Fatalf("release images pin Claude Code %s, but TestedVersion is %s", pinned, TestedVersion)
	}
	fake, err := os.ReadFile("../../cmd/fakeclaude/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if line := `"` + TestedVersion + ` (fakeclaude)"`; !strings.Contains(string(fake), line) {
		t.Fatalf("fakeclaude's --version does not print %s, TestedVersion", line)
	}
}
