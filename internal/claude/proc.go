package claude

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
)

// Proc is a running claude subprocess speaking stream-json on stdin/stdout.
type Proc interface {
	// Send writes one user message. Never call while a previous turn is in
	// flight; turn serialization is the caller's job.
	Send(text string) error
	// Events yields decoded stdout lines; closed when stdout closes.
	Events() <-chan Event
	// CloseStdin asks the process to finish up and exit cleanly (verified:
	// closing stdin after a result exits 0).
	CloseStdin() error
	Kill() error
	// Wait blocks until the process exits and returns its code and stderr tail.
	Wait() ExitInfo
	PID() int
}

type proc struct {
	*Stream
	cmd *exec.Cmd

	waitOnce sync.Once
	exit     ExitInfo
}

// Spawn starts a claude subprocess in workDir. It returns as soon as the
// process is started; the system/init event arrives on Events().
func Spawn(ctx context.Context, bin, workDir string, o Opts) (Proc, error) {
	args, err := Args(o)
	if err != nil {
		return nil, err
	}
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude: start %s: %w", bin, err)
	}

	p := &proc{cmd: cmd, Stream: Attach(stdin, stdout, stderr)}

	// If the surrounding context dies, take the subprocess with it.
	go func() {
		<-ctx.Done()
		_ = p.Kill()
	}()

	return p, nil
}

func (p *proc) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	return nil // Wait() escalates to SIGKILL if needed via process group teardown
}

func (p *proc) Wait() ExitInfo {
	p.waitOnce.Do(func() {
		err := p.cmd.Wait()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		p.exit = ExitInfo{Code: code, Stderr: p.StderrTail()}
	})
	return p.exit
}

func (p *proc) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
