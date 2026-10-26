// Package docker implements the SandboxRuntime seam on long-lived Docker
// containers driven through the docker CLI (ADR-0017, ADR-0018): one named
// container and one named volume per loop — the workstation — with a
// keepalive as PID 1, and claude exec'd inside per wake with its stdio held
// by the runner. The workstation survives sleeps, orchestrator restarts and
// daemon restarts; it is removed only by Destroy.
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
	"log/slog"
	"maps"
	"net"
	"os"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

const (
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
	egressImage  string // the allowlist proxy's image ("" leaves egress open)
	mcpPort      string // the hub's loop-facing MCP port, allowlisted on the gateway
	hook         string // the PreToolUse hook inside the workstation, "" for none

	healthMu  sync.Mutex
	healthTTL time.Duration
	healthAt  time.Time
	fleet     map[string]runtime.Health
	fleetErr  error

	// egressMu serializes the wall's check: two wakes, or a wake and a
	// login check, can both find a stale proxy, and two removals of it
	// collide.
	// A loop's own network is put up or taken down under its read lock: one
	// loop's beside another's is no collision, but beside a proxy being
	// replaced it is.
	egressMu sync.RWMutex
	// wallChecks and imageIDs share the reads a burst of wakes makes alike:
	// the egress wall's check, and an image's ID (#1133).
	wallChecks flight[struct{}]
	imageIDs   flight[string]

	// fleetEgress is the operator's extra hosts (#542), under egressFileMu:
	// the proxy's file holds them and is rewritten one change at a time.
	egressFileMu sync.Mutex
	fleetEgress  []string
	// fleetApplied is the fleet list the proxy's file last took, and
	// fleetAppliedAt when; zero until this run has written it.
	fleetApplied   []string
	fleetAppliedAt time.Time
	// egressFileWritten is whether this run has written the proxy's file
	// yet: until it has, the file may hold a previous run's entries.
	egressFileWritten bool

	sweepOnce      sync.Once // resolution containers a stopped hub left behind
	loginSweepOnce sync.Once // login-check containers, the same way
}

// Options configures a docker runtime. Only Bin and HealthTTL have defaults;
// the rest is the wirer's to decide.
type Options struct {
	Bin          string // the docker CLI (default "docker")
	DefaultImage string // provisioned for loops without an image of their own

	// EgressImage is the fleet's allowlist proxy (ADR-0028). Empty leaves
	// workstation egress open — the pre-0028 posture.
	EgressImage string
	// MCPPort is the port the hub serves /mcp on: the one port of the
	// operator's machine a workstation may reach (#238). The API and control
	// room listen elsewhere and are allowlisted nowhere.
	MCPPort string
	// Hook is the PreToolUse hook every claude runs, a path inside the
	// workstation (ADR-0042); "" runs none. The default image carries it at
	// WorkstationHook, and an image without it runs its loop unguarded.
	Hook string

	// HealthTTL bounds the batched liveness sweep; 0 means the default.
	HealthTTL time.Duration
}

// New returns a docker runtime shelling out to the docker CLI.
func New(opts Options) *Runtime {
	if opts.Bin == "" {
		opts.Bin = "docker"
	}
	if opts.HealthTTL <= 0 {
		opts.HealthTTL = defaultHealthTTL
	}
	return &Runtime{
		bin:          opts.Bin,
		defaultImage: opts.DefaultImage,
		egressImage:  opts.EgressImage,
		mcpPort:      opts.MCPPort,
		hook:         opts.Hook,
		healthTTL:    opts.HealthTTL,
	}
}

func (rt *Runtime) Kind() string { return "docker" }

// HasWorkstation is true: the container and its volume are the loop's own
// machine, which the operator can halt, start and rebuild under it.
func (rt *Runtime) HasWorkstation() bool { return true }

// Available reports whether the daemon is reachable — the cheap slice of
// Preflight, for validating a create request that names this runtime.
func (rt *Runtime) Available(ctx context.Context) error {
	if _, err := rt.EngineVersion(ctx); err != nil {
		return fmt.Errorf("docker daemon unreachable: %w", err)
	}
	return nil
}

// BridgeGateway is the default bridge's gateway address. A workstation's
// egress proxy reaches the hub as host.docker.internal, which the proxy is
// created to resolve to host-gateway: that address, on an engine that does
// not configure host-gateway-ip (#474).
func (rt *Runtime) BridgeGateway(ctx context.Context) (string, error) {
	out, err := rt.command(ctx, queryTimeout, "network", "inspect", "bridge", "--format", "{{range .IPAM.Config}}{{.Gateway}} {{end}}")
	if err != nil {
		return "", err
	}
	for _, field := range strings.Fields(string(out)) {
		if ip := net.ParseIP(field); ip != nil && ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("the docker bridge reports no IPv4 gateway (%q)", strings.TrimSpace(string(out)))
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
	version, err := rt.command(ctx, startTimeout, "run", "--pull=never", "--rm", rt.defaultImage, "claude", "--version")
	if err != nil {
		return "", fmt.Errorf("claude inside %s: %w", rt.defaultImage, err)
	}
	return strings.TrimSpace(string(version)), nil
}

// Ensure makes the loop's workstation exist and run. A workstation that was
// stopped (daemon restart before the restart policy existed, an operator's
// docker stop) or paused comes back here; one that is gone entirely is
// provisioned from scratch — its volume, if it survived, is reused.
//
// The wall comes first, on every wake and not only at a provision: a
// workstation that already exists still needs the proxy up, and the proxy
// still needs the fleet's extra hosts copied in, which a hub run whose
// workstations all survived from an earlier run would otherwise never do
// (#657).
//
// A workstation created from an older build of its image is replaced,
// keeping its volume: a rebuilt image (a new hook, a security fix) would
// otherwise reach only the loops provisioned after it (#715). The engine
// decides that no turn is in flight, never the container: what runs in it
// is the loop's to name, and a process it left behind must not keep it on
// an old build.
//
// The wall, the workstation and its image are read side by side, since no
// read waits on another: a wake that finds all three up costs the slowest
// read, not their sum (#1133). Nothing is provisioned, started or replaced
// until the wall is up.
func (rt *Runtime) Ensure(ctx context.Context, spec runtime.Spec) error {
	walled := make(chan error, 1)
	go func() { walled <- rt.ensureEgress(ctx) }()
	image := workstationImage(spec, rt.defaultImage)
	type imageRead struct {
		id  string
		err error
	}
	imaged := make(chan imageRead, 1)
	go func() {
		id, err := rt.imageID(ctx, image)
		imaged <- imageRead{id, err}
	}()
	name := containerName(spec.LoopID)
	state, err := rt.inspectState(ctx, name)
	current := <-imaged
	if wallErr := <-walled; wallErr != nil {
		return wallErr
	}
	// A workstation running on its loop network shows that network is up:
	// docker removes no network a running container is on, and the proxy
	// joined it before the workstation was provisioned. A wake that finds
	// one so leaves the network unread. A proxy disconnected from it by
	// hand is no longer rejoined by that wake, then, but by the next one
	// that provisions the workstation, or replaces the proxy.
	if err != nil || !state.Running || state.Paused || !slices.Contains(state.Networks, rt.loopNetwork(spec.LoopID)) {
		if netErr := rt.ensureLoopNetwork(ctx, spec.LoopID); netErr != nil {
			return netErr
		}
	}
	if err == nil {
		var stale bool
		if stale, err = rt.replaceStale(ctx, spec, state, current.id, current.err); err != nil || stale {
			return err
		}
	}
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

// replaceStale replaces the workstation, keeping its volume, when the image
// it names has been rebuilt since the container was created from it, or
// when it was provisioned under another posture, and reports whether it
// did. current is the ID the image names now, or the error reading it gave.
// An image that is not here any more replaces nothing: there would be
// nothing to provision from.
func (rt *Runtime) replaceStale(ctx context.Context, spec runtime.Spec, state *containerState, current string, err error) (bool, error) {
	image := workstationImage(spec, rt.defaultImage)
	switch {
	case errors.Is(err, errNotFound):
		return false, nil
	case err != nil:
		return false, err
	case current == state.Image && state.Posture == rt.posture(spec):
		return false, nil
	case current != state.Image:
		slog.Default().Info("workstation image changed", "loop", spec.LoopName, "image", image,
			"old", shortID(state.Image), "new", shortID(current))
	default:
		slog.Default().Info("workstation posture changed", "loop", spec.LoopName,
			"old", state.Posture, "new", rt.posture(spec))
	}
	if _, err := rt.command(ctx, startTimeout, "rm", "--force", containerName(spec.LoopID)); err != nil && !notFound(err) {
		return true, err
	}
	return true, rt.provision(ctx, spec)
}

// shortID is an image ID as docker lists it: 12 hex digits, no algorithm.
func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// provision creates a workstation. Ensure has put the wall up first: a
// workstation provisioned onto a network that does not exist yet would fail,
// and one provisioned while the proxy is down would simply have no way out.
func (rt *Runtime) provision(ctx context.Context, spec runtime.Spec) error {
	volumeArgv := append([]string{"volume", "create"}, labelArgs(spec)...)
	volumeArgv = append(volumeArgv, containerName(spec.LoopID))
	if _, err := rt.command(ctx, queryTimeout, volumeArgv...); err != nil {
		return err
	}
	if _, err := rt.command(ctx, runTimeout, runArgv(spec, rt.defaultImage, rt.workstationNetworkArgs(spec))...); err != nil {
		// A half-finished earlier provision can have left the container
		// created but not running; then the name is taken and starting the
		// existing one is the right move.
		if !nameInUse(err) {
			image := workstationImage(spec, rt.defaultImage)
			return withImageRemedy(err, image, image != rt.defaultImage)
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
	argv, err := execArgv(spec, rt.egressEnv(), rt.hook)
	if err != nil {
		return nil, err
	}
	env := spec.Env
	if spec.MCPConfig != "" {
		env = maps.Clone(spec.Env)
		if env == nil {
			env = map[string]string{}
		}
		env[mcpConfigEnv] = spec.MCPConfig
	}
	cmd := exec.Command(rt.bin, argv...)
	cmd.Env = environ(env)
	cmd.SysProcAttr = runtime.ChildAttr()

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

// Halt stops the container, leaving it and its volume in place: the operator
// switched the workstation off, and Ensure starts it again with everything
// still on it.
func (rt *Runtime) Halt(ctx context.Context, loopID string) error {
	if _, err := rt.command(ctx, startTimeout, "stop", containerName(loopID)); err != nil && !notFound(err) {
		return err
	}
	return nil
}

// Destroy removes the workstation, everything on its volume, and its loop
// network. Loop deletion and the operator's recreate control get here —
// never sleep or pause (ADR-0017).
func (rt *Runtime) Destroy(ctx context.Context, loopID string) error {
	name := containerName(loopID)
	if _, err := rt.command(ctx, startTimeout, "rm", "--force", name); err != nil && !notFound(err) {
		return err
	}
	if _, err := rt.command(ctx, queryTimeout, "volume", "rm", "--force", name); err != nil && !notFound(err) {
		return err
	}
	return rt.removeLoopNetwork(ctx, loopID)
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
// restarts, the loop's volume as its home, and no published ports
// (ADR-0017). It sits on network, and is denied NET_RAW (ADR-0028).
func runArgv(spec runtime.Spec, defaultImage string, network []string) []string {
	name := containerName(spec.LoopID)
	argv := []string{
		"run", "--pull=never", "--detach", "--init",
		"--name", name,
		"--restart", "unless-stopped",
		"--volume", name + ":" + runtime.WorkstationHome,
		// NET_RAW would let a workstation forge packets, and so pose as
		// another container on its network.
		"--cap-drop", "NET_RAW",
	}
	argv = append(argv, network...)
	argv = append(argv, labelArgs(spec)...)
	argv = append(argv, "--label", postureLabel+"="+workstationPosture(network))
	if spec.MemMB > 0 {
		argv = append(argv, "--memory", strconv.Itoa(spec.MemMB)+"m")
	}
	if spec.CPUs > 0 {
		argv = append(argv, "--cpus", strconv.FormatFloat(spec.CPUs, 'f', -1, 64))
	}
	return append(argv, workstationImage(spec, defaultImage), "sleep", "infinity")
}

// postureLabel records the posture a workstation was provisioned under:
// the capability it is denied and the network it sits on. Both are fixed
// at docker run, so a workstation under another posture, such as one from
// before loop networks, is replaced as a rebuilt image's is.
const postureLabel = "spool.ws.posture"

// workstationPosture is the posture label's value for a workstation
// provisioned with network.
func workstationPosture(network []string) string {
	return strings.Join(append([]string{"--cap-drop", "NET_RAW"}, network...), " ")
}

// workstationNetworkArgs puts a loop's workstation on its loop network.
func (rt *Runtime) workstationNetworkArgs(spec runtime.Spec) []string {
	return rt.networkArgs(rt.loopNetwork(spec.LoopID))
}

// posture is the posture a workstation for spec is provisioned under now.
func (rt *Runtime) posture(spec runtime.Spec) string {
	return workstationPosture(rt.workstationNetworkArgs(spec))
}

// workstationImage is the image a loop's workstation is provisioned from:
// its own, or the fleet's default.
func workstationImage(spec runtime.Spec, defaultImage string) string {
	if spec.Image != "" {
		return spec.Image
	}
	return defaultImage
}

// execArgv builds the docker exec for one wake. Env vars cross as
// value-less --env KEY flags — docker resolves them from the client
// process's environment, so credential values never appear in argv where
// host ps or logs could see them (ADR-0018). hook is the PreToolUse hook
// claude runs, "" for none.
func execArgv(spec runtime.Spec, egressEnv []string, hook string) ([]string, error) {
	opts := claude.Opts{
		Model:              spec.Model,
		Effort:             spec.Effort,
		NoSubagents:        spec.NoSubagents,
		SessionID:          spec.SessionID,
		ResumeID:           spec.ResumeID,
		AppendSystemPrompt: spec.AppendSystemPrompt,
		PartialMessages:    spec.PartialMessages,
		PreToolUseHook:     hook,
	}
	if spec.MCPConfig != "" {
		opts.MCPConfigPath = mcpConfigPath
	}
	claudeArgs, err := claude.Args(opts)
	if err != nil {
		return nil, err
	}
	argv := []string{"exec", "--interactive", "--workdir", spec.WorkDir}
	argv = append(argv, egressEnv...)
	for _, key := range sortedKeys(spec.Env) {
		argv = append(argv, "--env", key)
	}
	if spec.MCPConfig == "" {
		argv = append(argv, containerName(spec.LoopID), "claude")
		return append(argv, claudeArgs...), nil
	}
	// the shell's $0, then claude's own arguments as its "$@"
	argv = append(argv, "--env", mcpConfigEnv, containerName(spec.LoopID), "sh", "-c", writeMCPConfigThenClaude, "claude")
	return append(argv, claudeArgs...), nil
}

// WorkstationHook is where the default workstation image carries the
// PreToolUse hook (docker/workstation/Dockerfile).
const WorkstationHook = "/usr/local/bin/spool-hook"

// WorkstationGitHub is where the default workstation image carries
// spool-github, with its gh link ahead of the real gh on PATH
// (docker/workstation/Dockerfile, #1054).
const WorkstationGitHub = "/usr/local/bin/spool-github"

// mcpConfigPath is where a workstation keeps its loop's hub MCP config,
// written fresh at every wake.
const mcpConfigPath = runtime.WorkstationHome + "/.spool-mcp.json"

// mcpConfigEnv carries the loop's MCP config into the exec that starts
// claude. The config holds the loop's hub token, so it crosses the way the
// loop's other credentials do, as a value-less --env and never through host
// argv (ADR-0018).
const mcpConfigEnv = "SPOOL_MCP_CONFIG"

// writeMCPConfigThenClaude is what that exec runs: it writes the config
// owner-only, drops the variable so claude never holds it, and becomes
// claude. One exec where a write and a start were two, each a docker CLI
// call a burst of wakes waits on (#1133). The umask stays in the subshell:
// claude's own files are made as they always were.
const writeMCPConfigThenClaude = `(umask 077 && printf '%s' "$` + mcpConfigEnv + `" > ` + mcpConfigPath + `) && unset ` + mcpConfigEnv + ` && exec claude "$@"`

// fileCopyTimeout bounds one file's copy in or out: 20 MB through docker
// exec is seconds, and a daemon that takes minutes is not going to finish.
const fileCopyTimeout = 2 * time.Minute

// PutFile streams the file into the workstation over exec stdin, as the
// workstation's own user, so the loop owns what it is given. The same exec
// clears files there older than the hub keeps its own copies: the
// workstation's are copies of those, and would otherwise outlive them.
func (rt *Runtime) PutFile(ctx context.Context, loopID, hostPath, target string) error {
	file, err := os.Open(hostPath)
	if err != nil {
		return err
	}
	defer file.Close()
	// an in-container path: POSIX whatever the host is
	dir := path.Dir(target)
	_, err = rt.commandStream(ctx, fileCopyTimeout, file,
		"exec", "--interactive", containerName(loopID),
		"sh", "-c", `mkdir -p "$1" && find "$1" -type f -mtime +30 -delete 2>/dev/null; cat > "$2"`, "sh", dir, target)
	return err
}

// GetFile reads a file out of the workstation as its own user, so the loop
// can send only what it can read itself. The whole container is the loop's,
// so any path in it is one the loop owns. The script's exit status says why
// a file is refused; only then is it read, and never past limit.
func (rt *Runtime) GetFile(ctx context.Context, loopID, workDir, target string, limit int64) ([]byte, error) {
	out, err := rt.command(ctx, fileCopyTimeout,
		"exec", "--workdir", workDir, containerName(loopID),
		"sh", "-c", `[ -e "$1" ] || exit 64; [ -f "$1" ] || exit 65; [ "$(wc -c < "$1")" -le "$2" ] || exit 66; exec head -c "$2" -- "$1"`,
		"sh", target, strconv.FormatInt(limit, 10))
	var failure *cliError
	if errors.As(err, &failure) {
		switch failure.exitCode {
		case 64:
			return nil, runtime.ErrNoSuchFile
		case 65:
			return nil, runtime.ErrNotAFile
		case 66:
			return nil, runtime.ErrFileTooLarge
		}
	}
	return out, err
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

func sortedKeys(entries map[string]string) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
