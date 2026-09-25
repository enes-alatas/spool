package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"syscall"

	"github.com/enes-alatas/spool/internal/claude"
)

// resolveHome is the run's HOME inside the container: under /tmp, which the
// image's loop user can write, and gone with the container.
const resolveHome = "/tmp/spool-aux"

// resolveLifetime is how long a resolution container may live, enforced
// inside it by coreutils timeout (ADR-0018 item 7 requires coreutils). A hub
// stopped mid-run cannot remove its container, and the CLI in it would retry
// a refused connection forever; this ends it, and --rm then removes it.
const resolveLifetime = "60"

// resolveLabel marks resolution containers, so that one a stopped hub left
// behind can be found and removed.
const resolveLabel = "spool.resolve=1"

// resolveBaseURL is the only API the run is told of. The container has no
// network but its own loopback, and nothing listens on the discard port
// there, so any request the CLI makes is refused before it is sent.
const resolveBaseURL = "http://127.0.0.1:9"

// ResolveModel runs the default image's claude with model in a throwaway
// container that has no network at all (ADR-0033), and returns the id its
// init reports. Nothing can leave the container, whatever the CLI tries.
func (rt *Runtime) ResolveModel(ctx context.Context, model string) (string, error) {
	args, err := claude.ResolveArgs(model)
	if err != nil {
		return "", err
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	rt.sweepOnce.Do(func() { rt.sweepResolveLeftovers(ctx) })
	name := "spool-resolve-" + hex.EncodeToString(suffix)
	argv := []string{"run", "--rm", "-i", "--name", name, "--network", "none", "--label", resolveLabel}
	for _, kv := range claude.AuxEnv("", resolveHome, resolveBaseURL) {
		argv = append(argv, "-e", kv) // synthetic values only: nothing here is a secret
	}
	argv = append(argv, "--workdir", "/tmp", rt.defaultImage, "timeout", resolveLifetime, "claude")
	argv = append(argv, args...)

	cmd := exec.Command(rt.bin, argv...)
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
		return "", fmt.Errorf("docker run %s: %w", name, err)
	}
	defer func() {
		// Killing the client leaves the container running (ADR-0018 item 4),
		// so the container is removed by name.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_, _ = rt.command(context.Background(), queryTimeout, "rm", "-f", name)
	}()
	return claude.ResolvedModel(ctx, claude.Attach(stdin, stdout, stderr))
}

// sweepResolveLeftovers removes resolution containers a stopped hub left
// behind: created but never started, so --rm never applied to them. Only
// stopped ones older than any run could be are taken, so a run another hub
// on this daemon has in flight is left alone. Best effort, once per hub.
func (rt *Runtime) sweepResolveLeftovers(ctx context.Context) {
	_, _ = rt.command(ctx, queryTimeout, "container", "prune", "--force",
		"--filter", "label="+resolveLabel, "--filter", "until=5m")
}
