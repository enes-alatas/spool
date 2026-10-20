//go:build !linux

package cloud

import (
	"errors"
	"os"
	"os/exec"
)

// startOnTerminal needs Linux's pseudo-terminals, where the hub ships.
func startOnTerminal(*exec.Cmd) (*os.File, error) {
	return nil, errors.New("cloud: starting a session through the CLI needs Linux")
}

// killTerminal ends a command startOnTerminal started.
func killTerminal(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
