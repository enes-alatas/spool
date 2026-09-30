// Package bare runs a loop's claude process directly on the host: no
// container, and so no containment at all — the workspace directory is a
// working directory, not a boundary. It is the explicitly-chosen
// implementation of the SandboxRuntime seam (ADR-0010), never a fallback
// something selects for you: the operator asks for it with --runtime bare or
// --allow-bare, and the local edition badges loops using it as uncontained
// (ADR-0017). The hosted service does not ship it at all.
//
// There is no workstation to provision or destroy — the host is the
// workstation — so Ensure, Destroy and Health are trivial, and the power
// controls do not apply at all.
package bare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

	cfgOnce sync.Once
	cfgDir  string // private dir for per-loop MCP config files
	cfgErr  error

	keepOut string // the hub's data directory, which GetFile never reads
}

// New returns a bare runtime spawning bin (default "claude").
func New(bin string) *Runtime {
	if bin == "" {
		bin = "claude"
	}
	return &Runtime{bin: bin}
}

// KeepOut names the hub's data directory: the database and every token
// are in it, so no loop may send a file from it. Call it before the
// runtime is used.
func (host *Runtime) KeepOut(dir string) { host.keepOut = dir }

func (host *Runtime) Kind() string { return "bare" }

func (host *Runtime) Preflight(ctx context.Context) (string, error) {
	return claude.Preflight(ctx, host.bin)
}

// Ensure is a no-op: the host is always provisioned.
func (host *Runtime) Ensure(ctx context.Context, spec runtime.Spec) error { return nil }

// Halt has nothing to stop: halting the host is not ours to do.
func (host *Runtime) Halt(ctx context.Context, loopID string) error {
	return runtime.ErrUnsupported
}

// Destroy is a no-op: nothing outside the process belongs to us. A loop's
// workspace directory is the operator's, and its worktree is removed
// explicitly on delete, not here.
func (host *Runtime) Destroy(ctx context.Context, loopID string) error { return nil }

// HasWorkstation is false: the host is the workstation, so there is nothing
// the operator's power controls could halt or rebuild.
func (host *Runtime) HasWorkstation() bool { return false }

// PutFile has nothing to do for the path a bare loop is shown, which is the
// hub's own copy on the same host. Any other path is a plain copy.
func (host *Runtime) PutFile(ctx context.Context, loopID, hostPath, path string) error {
	if filepath.Clean(hostPath) == filepath.Clean(path) {
		return nil
	}
	source, err := os.Open(hostPath)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	target, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return err
	}
	return target.Close()
}

// GetFile reads a file inside the loop's working directory, and nothing
// else: the host is the workstation, and it holds the operator's keys and
// the hub's own data beside the loop's work (#123). The open goes through an
// os.Root on workDir, which refuses a path that leaves it, by ".." or by a
// symlink, at the open itself, so no check made before it can be raced.
func (host *Runtime) GetFile(ctx context.Context, loopID, workDir, path string, limit int64) ([]byte, error) {
	if workDir == "" || host.holdsHubData(workDir) {
		return nil, runtime.ErrNotOwned
	}
	rel, ok := insideDir(workDir, path)
	if !ok {
		return nil, runtime.ErrNotOwned
	}
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(rel)
	if err != nil {
		return nil, openError(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, runtime.ErrNotAFile
	}
	// A hard link is the one way to put a file from outside inside that
	// os.Root cannot see: it is the same file under a second name. A file
	// the loop wrote has one name.
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
		return nil, fmt.Errorf("%w: %s has other names", runtime.ErrNotOwned, rel)
	}
	if info.Size() > limit {
		return nil, runtime.ErrFileTooLarge
	}
	// A file that grew since the Stat is caught here instead.
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, runtime.ErrFileTooLarge
	}
	return body, nil
}

// holdsHubData reports whether workDir contains the hub's data directory,
// as an operator-chosen workspace such as a home directory can. The os.Root
// keeps a read inside workDir, not out of a directory within it, so a loop
// working there may send nothing at all.
func (host *Runtime) holdsHubData(workDir string) bool {
	if host.keepOut == "" {
		return false
	}
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return filepath.Clean(path)
	}
	rel, err := filepath.Rel(resolve(workDir), resolve(host.keepOut))
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// insideDir is path relative to dir, if it names something inside dir
// lexically. An absolute path may spell dir through a symlink, so it is
// tried against dir as resolved too. The os.Root that opens the result is
// what enforces the boundary; this only turns a path into its name there.
func insideDir(dir, path string) (string, bool) {
	if !filepath.IsAbs(path) {
		return filepath.Clean(path), filepath.IsLocal(path)
	}
	dirs := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		dirs = append(dirs, resolved)
	}
	for _, candidate := range dirs {
		if rel, err := filepath.Rel(candidate, path); err == nil && filepath.IsLocal(rel) {
			return rel, true
		}
	}
	return "", false
}

// openError says why os.Root would not open rel. A missing file says so.
// Anything else is a path the loop cannot send from: one that escapes, an
// absolute symlink (which os.Root refuses even when it points back inside),
// or a file the loop cannot read.
func openError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return runtime.ErrNoSuchFile
	}
	return fmt.Errorf("%w: %w", runtime.ErrNotOwned, err)
}

// Health is always up: if the orchestrator is running, so is the host.
func (host *Runtime) Health(ctx context.Context, loopID string) (runtime.Health, error) {
	return runtime.Health{Up: true}, nil
}

// Start spawns claude in the loop's workspace. It returns as soon as the
// process is started; the system/init event arrives on Events(). ctx governs
// the spawn attempt only: there is deliberately no kill-on-cancel goroutine
// — the process outlives the call and is torn down by Kill, by Wait, or by
// Pdeathsig when the orchestrator dies.
func (host *Runtime) Start(ctx context.Context, spec runtime.Spec) (runtime.Proc, error) {
	opts := claude.Opts{
		Model:              spec.Model,
		Effort:             spec.Effort,
		SessionID:          spec.SessionID,
		ResumeID:           spec.ResumeID,
		AppendSystemPrompt: spec.AppendSystemPrompt,
		PartialMessages:    spec.PartialMessages,
	}
	if spec.MCPConfig != "" {
		// The config carries the loop's hub token: a 0600 file keeps it out
		// of argv, where any host process could ps it. The file lives in a
		// 0700 directory MkdirTemp mints for this run — a predictable name
		// directly under the shared temp dir could be pre-created or
		// symlinked by another host user, handing them the token. One
		// stable path per loop — each wake overwrites the last.
		host.cfgOnce.Do(func() {
			host.cfgDir, host.cfgErr = os.MkdirTemp("", "spool-mcp-")
		})
		if host.cfgErr != nil {
			return nil, fmt.Errorf("mcp config dir: %w", host.cfgErr)
		}
		path := filepath.Join(host.cfgDir, spec.LoopID+".json")
		if err := os.WriteFile(path, []byte(spec.MCPConfig), 0o600); err != nil {
			return nil, fmt.Errorf("write mcp config: %w", err)
		}
		opts.MCPConfigPath = path
	}
	args, err := claude.Args(opts)
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

	return &hostProc{cmd: cmd, Stream: claude.Attach(stdin, stdout, stderr)}, nil
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

// ResolveModel runs the host's claude with model and returns the id its init
// reports (ADR-0033). The run's environment is built from nothing, and its
// base URL is a loopback port held for the run and never read: no other
// process can be listening there, and nothing the CLI sends is answered.
func (host *Runtime) ResolveModel(ctx context.Context, model string) (string, error) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = hold.Close() }()
	home, err := os.MkdirTemp("", "spool-aux-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(home) }()
	args, err := claude.ResolveArgs(model)
	if err != nil {
		return "", err
	}

	cmd := exec.Command(host.bin, args...)
	cmd.Dir = home
	cmd.Env = claude.AuxEnv(os.Getenv("PATH"), home, "http://"+hold.Addr().String())
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude: start %s: %w", host.bin, err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	return claude.ResolvedModel(ctx, claude.Attach(stdin, stdout, stderr))
}
