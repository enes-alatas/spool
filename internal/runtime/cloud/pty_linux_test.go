package cloud

import (
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestStartOnTerminalGivesTheCommandATerminal(t *testing.T) {
	cmd := exec.Command("sh", "-c", `[ -t 0 ] && [ -t 1 ] && echo on-a-terminal; stty size`)
	terminal, err := startOnTerminal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	out, _ := io.ReadAll(terminal) // EIO once the command's side closes
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the command failed: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "on-a-terminal") || !strings.Contains(string(out), "50 200") {
		t.Errorf("the command saw %q, want a 50x200 terminal on stdin and stdout", out)
	}
}
