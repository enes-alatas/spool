// Package docker implements the SandboxRuntime seam on long-lived Docker
// containers driven through the docker CLI (ADR-0017, ADR-0018): one named
// container and one named volume per loop — the workstation — with a
// keepalive as PID 1, and claude exec'd inside per wake with its stdio held
// by the runner. The workstation survives sleeps, orchestrator restarts and
// daemon restarts; it is removed only by PowerOff.
//
// Everything is a docker CLI subprocess: `exec -i` hands over the inner
// process's stdio as ordinary pipes (exactly what claude.Attach needs), and
// structured state is read via `--format '{{json .}}'`. No TTY is ever
// allocated — it would merge stdout and stderr and break both the
// stream-json protocol and session-lost detection.
package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

const (
	// home is where the loop's volume mounts inside every workstation — part
	// of the image convention fixed by ADR-0018.
	home = "/home/loop"

	// orphanGrace is how long claude inside a workstation gets to honour
	// SIGTERM before it is killed outright.
	orphanGrace = 5 * time.Second

	// defaultHealthTTL bounds how often the batched fleet sweep may run.
	defaultHealthTTL = 10 * time.Second
)

// Per-call deadlines: quick state reads, container starts, and a run that
// may have to pull the image first.
const (
	queryTimeout = 15 * time.Second
	startTimeout = 30 * time.Second
	runTimeout   = 2 * time.Minute
)

// Runtime is the docker implementation of runtime.Runtime.
type Runtime struct {
	bin          string // the docker CLI
	defaultImage string // provisioned when a loop doesn't override it

	healthMu  sync.Mutex
	healthTTL time.Duration
	healthAt  time.Time
	fleet     map[string]runtime.Health
	fleetErr  error
}

// New returns a docker runtime shelling out to bin (default "docker"),
// provisioning defaultImage for loops without an image of their own.
// healthTTL bounds the batched liveness sweep; 0 means the default.
func New(bin, defaultImage string, healthTTL time.Duration) *Runtime {
	if bin == "" {
		bin = "docker"
	}
	if healthTTL <= 0 {
		healthTTL = defaultHealthTTL
	}
	return &Runtime{bin: bin, defaultImage: defaultImage, healthTTL: healthTTL}
}

func (rt *Runtime) Kind() string { return "docker" }

// Available reports whether the daemon is reachable — the cheap slice of
// Preflight, for validating a create request that names this runtime.
func (rt *Runtime) Available(ctx context.Context) error {
	if _, err := rt.command(ctx, queryTimeout, "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("docker daemon unreachable: %w", err)
	}
	return nil
}

// Preflight verifies the CLI can reach a daemon. The claude version is
// best-effort: until the default image exists locally (#14) there is nothing
// to run it in, and that is a warning for the wirer, not a failure here.
func (rt *Runtime) Preflight(ctx context.Context) (string, error) {
	if err := rt.Available(ctx); err != nil {
		return "", err
	}
	if _, err := rt.command(ctx, queryTimeout, "image", "inspect", rt.defaultImage); err != nil {
		return "", nil
	}
	version, err := rt.command(ctx, startTimeout, "run", "--rm", rt.defaultImage, "claude", "--version")
	if err != nil {
		return "", fmt.Errorf("claude inside %s: %w", rt.defaultImage, err)
	}
	return strings.TrimSpace(string(version)), nil
}

// Ensure makes the loop's workstation exist and run. A workstation that was
// stopped (daemon restart before the restart policy existed, an operator's
// docker stop) or paused comes back here; one that is gone entirely is
// provisioned from scratch — its volume, if it survived, is reused.
func (rt *Runtime) Ensure(ctx context.Context, spec runtime.Spec) error {
	name := containerName(spec.LoopID)
	state, err := rt.inspectState(ctx, name)
	switch {
	case errors.Is(err, errNotFound):
		return rt.provision(ctx, spec)
	case err != nil:
		return err
	case state.Paused:
		_, err := rt.command(ctx, startTimeout, "unpause", name)
		return err
	case state.Running:
		return nil
	default:
		_, err := rt.command(ctx, startTimeout, "start", name)
		return err
	}
}

func (rt *Runtime) provision(ctx context.Context, spec runtime.Spec) error {
	volumeArgv := append([]string{"volume", "create"}, labelArgs(spec)...)
	volumeArgv = append(volumeArgv, containerName(spec.LoopID))
	if _, err := rt.command(ctx, queryTimeout, volumeArgv...); err != nil {
		return err
	}
	if _, err := rt.command(ctx, runTimeout, runArgv(spec, rt.defaultImage)...); err != nil {
		// A half-finished earlier provision can have left the container
		// created but not running; then the name is taken and starting the
		// existing one is the right move.
		if !nameInUse(err) {
			return err
		}
		_, err := rt.command(ctx, startTimeout, "start", containerName(spec.LoopID))
		return err
	}
	return nil
}

// Start execs claude inside the workstation, the client's pipes carrying
// the inner process's stdio verbatim. There is deliberately no
// kill-on-context-cancel goroutine: killing the exec client would not touch
// the inner claude anyway (ADR-0018) — Kill and boot-time Reap go through
// the wall instead.
func (rt *Runtime) Start(ctx context.Context, spec runtime.Spec) (runtime.Proc, error) {
	argv, err := execArgv(spec)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(rt.bin, argv...)
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
		return nil, fmt.Errorf("docker exec %s: %w", containerName(spec.LoopID), err)
	}
	return &workstationProc{
		Stream:    claude.Attach(stdin, stdout, stderr),
		cmd:       cmd,
		runtime:   rt,
		container: containerName(spec.LoopID),
	}, nil
}

// Reap terminates any claude left inside the workstation by a previous
// orchestrator run. The recorded pid is meaningless across the wall —
// reaping keys on the workstation itself.
func (rt *Runtime) Reap(ctx context.Context, loopID string, pid int) error {
	state, err := rt.inspectState(ctx, containerName(loopID))
	if errors.Is(err, errNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !state.Running || state.Paused {
		return nil
	}
	return rt.stopClaude(ctx, containerName(loopID))
}

// PowerOff destroys the workstation and everything on its volume. Only loop
// deletion gets here — never sleep or pause (ADR-0017).
func (rt *Runtime) PowerOff(ctx context.Context, loopID string) error {
	name := containerName(loopID)
	if _, err := rt.command(ctx, startTimeout, "rm", "--force", name); err != nil && !notFound(err) {
		return err
	}
	if _, err := rt.command(ctx, queryTimeout, "volume", "rm", "--force", name); err != nil && !notFound(err) {
		return err
	}
	return nil
}

// stopClaude terminates claude inside a workstation: TERM now, KILL after a
// grace (the bare runtime's orphan shape). pkill matches the process name —
// the ADR-0018 image convention — and its exit code 1 (nothing matched)
// means already gone, which is success.
func (rt *Runtime) stopClaude(ctx context.Context, container string) error {
	if err := rt.pkill(ctx, container, "-TERM"); err != nil {
		return err
	}
	go func() {
		time.Sleep(orphanGrace)
		_ = rt.pkill(context.Background(), container, "-KILL")
	}()
	return nil
}

func (rt *Runtime) pkill(ctx context.Context, container, signal string) error {
	_, err := rt.command(ctx, queryTimeout, "exec", container, "pkill", signal, "-x", "claude")
	var cli *cliError
	if errors.As(err, &cli) && cli.exitCode == 1 {
		return nil
	}
	return err
}

// containerName names a loop's workstation — its container and its volume.
func containerName(loopID string) string { return "spool-ws-" + loopID }

func labelArgs(spec runtime.Spec) []string {
	return []string{
		"--label", "spool.loop.id=" + spec.LoopID,
		"--label", "spool.loop.name=" + spec.LoopName,
	}
}

// runArgv builds the docker run that provisions a workstation: a keepalive
// as PID 1 under tini (--init reaps orphans that exec'd claudes leave
// behind), restart unless-stopped so the workstation outlives daemon
// restarts, and the loop's volume as its home. Open egress, no published
// ports (ADR-0017).
func runArgv(spec runtime.Spec, defaultImage string) []string {
	name := containerName(spec.LoopID)
	argv := []string{
		"run", "--detach", "--init",
		"--name", name,
		"--restart", "unless-stopped",
		"--volume", name + ":" + home,
	}
	argv = append(argv, labelArgs(spec)...)
	if spec.MemMB > 0 {
		argv = append(argv, "--memory", strconv.Itoa(spec.MemMB)+"m")
	}
	if spec.CPUs > 0 {
		argv = append(argv, "--cpus", strconv.FormatFloat(spec.CPUs, 'f', -1, 64))
	}
	image := spec.Image
	if image == "" {
		image = defaultImage
	}
	return append(argv, image, "sleep", "infinity")
}

// execArgv builds the docker exec for one wake. Env vars cross as
// value-less --env KEY flags — docker resolves them from the client
// process's environment, so credential values never appear in argv where
// host ps or logs could see them (ADR-0018).
func execArgv(spec runtime.Spec) ([]string, error) {
	claudeArgs, err := claude.Args(claude.Opts{
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
	argv := []string{"exec", "--interactive", "--workdir", spec.WorkDir}
	for _, key := range sortedKeys(spec.Env) {
		argv = append(argv, "--env", key)
	}
	argv = append(argv, containerName(spec.LoopID), "claude")
	return append(argv, claudeArgs...), nil
}

// environ returns the exec client's environment: inherited, plus the loop's
// own variables for docker to resolve the --env KEY flags from.
func environ(extra map[string]string) []string {
	if len(extra) == 0 {
		return nil
	}
	env := os.Environ()
	for _, key := range sortedKeys(extra) {
		env = append(env, key+"="+extra[key])
	}
	return env
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
