package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// loginCheckLabel marks login-check containers; the resolution sweep's
// reasoning holds for them too (sweepLoginCheckLeftovers).
const loginCheckLabel = "spool.login-check=1"

// loginCheckLifetime is how long a login-check container may live, enforced
// inside it as resolveLifetime is. The bound that governs a check is the
// hub's own, loop's loginCheckTimeout (90 s): it cancels the run, and the
// container is removed with it. This one sits above it and only ends a
// container whose hub stopped mid-check, which --rm then removes.
const loginCheckLifetime = "120"

// CheckLogin runs the default image's claude under token in a throwaway
// container behind the same egress wall as a workstation, so it reaches the
// API the way a loop does and nothing a loop could not (ADR-0044). The token
// crosses as a value-less --env, resolved from the client's environment, so
// it is never in argv for ps to show. HOME and the config dir are fresh
// under /tmp and go with the container.
func (rt *Runtime) CheckLogin(ctx context.Context, token string) (claude.LoginCheck, error) {
	if token == "" {
		return claude.LoginCheck{}, fmt.Errorf("docker: login check: no setup-token")
	}
	if err := rt.ensureEgress(ctx); err != nil {
		return claude.LoginCheck{}, err
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return claude.LoginCheck{}, err
	}
	rt.loginSweepOnce.Do(func() { rt.sweepLoginCheckLeftovers(ctx) })
	name := "spool-login-check-" + hex.EncodeToString(suffix)
	argv := []string{"run", "--rm", "-i", "--name", name, "--label", loginCheckLabel}
	argv = append(argv, rt.networkArgs()...)
	argv = append(argv, rt.egressEnv()...)
	argv = append(argv,
		"--env", "HOME="+auxHome,
		"--env", "CLAUDE_CONFIG_DIR="+auxHome+"/config",
		"--env", "CLAUDE_CODE_OAUTH_TOKEN",
		"--workdir", "/tmp", rt.defaultImage, "timeout", loginCheckLifetime, "claude")
	argv = append(argv, claude.LoginCheckArgs()...)

	cmd := exec.Command(rt.bin, argv...)
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+token)
	cmd.SysProcAttr = runtime.ChildAttr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return claude.LoginCheck{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return claude.LoginCheck{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return claude.LoginCheck{}, err
	}
	if err := cmd.Start(); err != nil {
		return claude.LoginCheck{}, fmt.Errorf("docker run %s: %w", name, err)
	}
	defer func() {
		// Killing the client leaves the container running (ADR-0018),
		// so the container is removed by name.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_, _ = rt.command(context.Background(), queryTimeout, "rm", "-f", name)
	}()
	return claude.CheckLogin(ctx, claude.Attach(stdin, stdout, stderr))
}

// sweepLoginCheckLeftovers removes login-check containers a stopped hub
// left behind, as sweepResolveLeftovers does resolution ones.
func (rt *Runtime) sweepLoginCheckLeftovers(ctx context.Context) {
	_, _ = rt.command(ctx, queryTimeout, "container", "prune", "--force",
		"--filter", "label="+loginCheckLabel, "--filter", "until=5m")
}
