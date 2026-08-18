// Package bare runs a loop's claude process directly on the host: no
// container, no isolation beyond the workspace directory. It is the
// zero-dependency fallback of the SandboxRuntime seam (ADR-0010) and the
// local edition badges loops using it as uncontained (ADR-0017); the hosted
// service does not ship it at all.
//
// There is no workstation to provision or destroy — the host is the
// workstation — so Ensure, PowerOff and Health are trivial.
package bare

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// orphanGrace is how long a leftover process from a previous run gets to
// honour SIGTERM before it is killed outright.
const orphanGrace = 5 * time.Second

// Runtime is the host-subprocess implementation of runtime.Runtime.
type Runtime struct {
	bin string // path to the claude binary
}

// New returns a bare runtime spawning bin (default "claude").
func New(bin string) *Runtime {
	if bin == "" {
		bin = "claude"
	}
	return &Runtime{bin: bin}
}

func (host *Runtime) Kind() string { return "bare" }

func (host *Runtime) Preflight(ctx context.Context) (string, error) {
	return claude.Preflight(ctx, host.bin)
}

// Ensure is a no-op: the host is always provisioned.
func (host *Runtime) Ensure(ctx context.Context, spec runtime.Spec) error { return nil }

// PowerOff is a no-op: nothing outside the process belongs to us. A loop's
// workspace directory is the operator's, and its worktree is removed
// explicitly on delete, not here.
func (host *Runtime) PowerOff(ctx context.Context, loopID string) error { return nil }

// Health is always up: if the orchestrator is running, so is the host.
func (host *Runtime) Health(ctx context.Context, loopID string) (runtime.Health, error) {
	return runtime.Health{Up: true}, nil
}

// Start spawns claude in the loop's workspace. It returns as soon as the
// process is started; the system/init event arrives on Events().
func (host *Runtime) Start(ctx context.Context, spec runtime.Spec) (runtime.Proc, error) {
	args, err := claude.Args(claude.Opts{
		Model:              spec.Model,
		Effort:             spec.Effort,
		SessionID:          spec.SessionID,
		ResumeID:           spec.ResumeID,
		AppendSystemPrompt: spec.AppendSystemPrompt,
		PartialMessages:    spec.PartialMessages,
	})
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(host.bin, args...)
	cmd.Dir = spec.WorkDir
	cmd.Env = environ(spec.Env)
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
		return nil, fmt.Errorf("claude: start %s: %w", host.bin, err)
	}

	process := &hostProc{cmd: cmd, Stream: claude.Attach(stdin, stdout, stderr)}

	// If the surrounding context dies, take the subprocess with it.
	go func() {
		<-ctx.Done()
		_ = process.Kill()
	}()

	return process, nil
}

// Reap terminates a claude process left behind by a previous orchestrator
// run. Pdeathsig should have handled it, but belt and braces.
func (host *Runtime) Reap(ctx context.Context, loopID string, pid int) error {
	if pid <= 1 {
		return nil
	}
	// only signal if the pid still exists and we own it
	if err := syscall.Kill(pid, 0); err != nil {
		return nil
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	go func() {
		time.Sleep(orphanGrace)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}()
	return nil
}

// environ returns the child's environment: inherited, plus the loop's own
// variables. A loop with no variables of its own inherits ours unchanged.
func environ(extra map[string]string) []string {
	if len(extra) == 0 {
		return nil
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := os.Environ()
	for _, key := range keys {
		env = append(env, key+"="+extra[key])
	}
	return env
}

// hostProc is a claude subprocess: the stream-json protocol from
// internal/claude plus the process control only a host spawner can provide.
type hostProc struct {
	*claude.Stream
	cmd *exec.Cmd

	waitOnce sync.Once
	exit     claude.ExitInfo
}

func (proc *hostProc) Kill() error {
	if proc.cmd.Process == nil {
		return nil
	}
	_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	return nil // Wait() escalates to SIGKILL if needed via process group teardown
}

func (proc *hostProc) Wait() claude.ExitInfo {
	proc.waitOnce.Do(func() {
		err := proc.cmd.Wait()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		proc.exit = claude.ExitInfo{Code: code, Stderr: proc.StderrTail()}
	})
	return proc.exit
}

func (proc *hostProc) PID() int {
	if proc.cmd.Process == nil {
		return 0
	}
	return proc.cmd.Process.Pid
}
