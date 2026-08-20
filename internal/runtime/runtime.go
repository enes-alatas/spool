// Package runtime defines the SandboxRuntime seam (ADR-0004, ADR-0010,
// ADR-0017): where a loop's claude process actually runs. Implementations
// live in subpackages — bare (a host subprocess) today, docker (a long-lived
// container and volume per loop) at L1 — and the runner talks only to this
// interface.
//
// The seam is deliberately hub-free: it knows nothing of the store, the bus
// or the API, and everything crossing it is plain data. That is what keeps
// the service-era runner extraction a transport substitution rather than a
// redesign (ADR-0004).
package runtime

import (
	"context"
	"errors"

	"github.com/enes-alatas/spool/internal/claude"
)

// ErrUnsupported is what a runtime returns for an operation its kind of
// workstation cannot do — halting the host it runs on, say.
var ErrUnsupported = errors.New("runtime: operation not supported")

// WorkstationHome is where a containerized runtime mounts the loop's volume
// and where its claude runs — the image convention fixed by ADR-0018. Bare
// loops have host workspaces instead.
const WorkstationHome = "/home/loop"

// Spec is everything a runtime needs for one wake of one loop. Plain data —
// no live channels, no callbacks, no store types.
type Spec struct {
	LoopID   string
	LoopName string // for workstation naming and diagnostics

	// WorkDir is claude's working directory, in the runtime's own filesystem
	// (a host path for bare, an in-container path for docker). It must be
	// stable across wakes: sessions are keyed by cwd.
	WorkDir string

	// Workstation config (ADR-0017). Runtimes without a workstation to
	// provision (bare) ignore all three.
	Image string  // workstation image; never empty for runtimes that use it
	MemMB int     // workstation memory limit
	CPUs  float64 // workstation CPU limit

	Model  string
	Effort string // ""|low|medium|high|xhigh|max

	// Exactly one of SessionID (fresh session with a pre-chosen uuid) or
	// ResumeID (continue an existing session) is set.
	SessionID string
	ResumeID  string

	AppendSystemPrompt string
	PartialMessages    bool

	// Env carries per-loop credentials into the workstation — the claude
	// OAuth token (#11) and secret env vars (#12). Never logged.
	Env map[string]string
}

// Health is a workstation's liveness as the control room reports it.
type Health struct {
	Up     bool
	Detail string // human-readable reason when Up is false
}

// Runtime owns a loop's workstation: it provisions it, execs claude inside
// it with the runner holding that process's stdio, and watches it live.
//
// A workstation is long-lived (ADR-0017): it survives sleeps, orchestrator
// restarts and pauses, and goes away only with the loop. A wake is Ensure
// followed by Start; a sleep ends the Proc and nothing else.
type Runtime interface {
	// Kind names the implementation — "bare", "docker" — for logs and the
	// control room's containment badge.
	Kind() string

	// Preflight verifies the runtime is usable and returns the claude version
	// it will run, or "" when that can't be known yet (e.g. the docker
	// runtime before its image exists locally). Called once at boot; the
	// wirer treats failure as fatal for the default runtime and as a warning
	// for the others.
	Preflight(ctx context.Context) (version string, err error)

	// Ensure makes the loop's workstation exist and be ready to exec into.
	// Idempotent and called before every wake, so a workstation that died
	// while the loop slept comes back by itself.
	Ensure(ctx context.Context, spec Spec) error

	// Start execs claude inside the workstation and hands back the live
	// process with its stdio attached. ctx governs the start attempt only:
	// the process outlives the call and is torn down through Proc.Kill, not
	// by cancelling ctx.
	Start(ctx context.Context, spec Spec) (Proc, error)

	// Reap cleans up an execution left behind by a previous orchestrator run,
	// identified by the pid the store recorded for it. Called during boot
	// recovery, before the loop's first wake.
	Reap(ctx context.Context, loopID string, pid int) error

	// Halt stops the workstation, keeping everything on it. The operator's
	// power-off control comes here; a halted workstation comes back with
	// Ensure, carrying its state. Runtimes with no workstation of their own
	// return ErrUnsupported.
	Halt(ctx context.Context, loopID string) error

	// Destroy removes the workstation and everything persisted inside it.
	// Loop deletion goes here, and so does the operator's recreate control —
	// deliberately never sleep or pause (ADR-0017).
	Destroy(ctx context.Context, loopID string) error

	// HasWorkstation reports whether the loop's workstation is a thing of
	// this runtime's own, separable from the loop: something that can be
	// halted, started and rebuilt while the loop lives on. False for bare,
	// where the workstation is the operator's host and the power controls
	// have nothing to act on.
	HasWorkstation() bool

	// Health reports whether the loop's workstation is up.
	Health(ctx context.Context, loopID string) (Health, error)
}

// Proc is a claude process running inside a workstation, speaking
// stream-json on stdio the runner owns.
type Proc interface {
	// Send writes one user message. Never call while a previous turn is in
	// flight; turn serialization is the caller's job.
	Send(text string) error
	// Events yields decoded stdout lines; closed when stdout closes.
	Events() <-chan claude.Event
	// CloseStdin asks claude to finish up and exit cleanly.
	CloseStdin() error
	Kill() error
	// Wait blocks until the process exits and returns its code and stderr tail.
	Wait() claude.ExitInfo
	// PID identifies the execution for boot-time reaping. Runtimes that have
	// no host pid to give report 0.
	PID() int
}
