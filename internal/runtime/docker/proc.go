package docker

import (
	"context"
	"os/exec"
	"sync"

	"github.com/enes-alatas/spool/internal/claude"
)

// workstationProc is claude running inside a workstation: the stream-json
// protocol over the docker exec client's pipes, plus the process control
// that must go through the wall — docker has no kill-an-exec API and
// killing the client would leave the inner process running (ADR-0018), so
// Kill signals claude by name inside the container instead.
type workstationProc struct {
	*claude.Stream
	cmd       *exec.Cmd
	runtime   *Runtime
	container string

	waitOnce sync.Once
	exit     claude.ExitInfo
}

func (proc *workstationProc) Kill() error {
	return proc.runtime.stopClaude(context.Background(), proc.container)
}

func (proc *workstationProc) Wait() claude.ExitInfo {
	proc.waitOnce.Do(func() {
		proc.AwaitStderr(claude.StderrSettle)
		err := proc.cmd.Wait()
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				// docker exec propagates the inner exit code; 125, 126 and
				// 127 are docker's own failures, told apart by the stderr
				// tail the crash path already records.
				code = exitErr.ExitCode()
			} else {
				code = -1
			}
		}
		proc.exit = claude.ExitInfo{Code: code, Stderr: proc.StderrTail()}
	})
	return proc.exit
}

// PID is the docker exec client's host pid. Claude's own pid lives inside
// the container and means nothing out here, so boot reaping keys on the
// workstation. A pid stored for the loop says only that a claude was
// running in it when the hub went down (#1134).
func (proc *workstationProc) PID() int { return proc.cmd.Process.Pid }
