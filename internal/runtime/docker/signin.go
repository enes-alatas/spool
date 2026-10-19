package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/runtime"
)

// signInLabel marks the containers the hub's own claude auth runs in. None
// needs sweeping: timeout ends one a stopped hub left, and --rm removes it.
const signInLabel = "spool.sign-in=1"

// signInConfigDir is where the sign-in's config directory is mounted
// inside its container.
const signInConfigDir = "/sign-in"

// ImageReady reports whether the hub's claude auth can run in the default
// image: the daemon answers and the image is here. It never pulls one.
func (rt *Runtime) ImageReady(ctx context.Context) error {
	if _, err := rt.command(ctx, queryTimeout, "image", "inspect", "--format", "{{.Id}}", rt.defaultImage); err != nil {
		return fmt.Errorf("docker: image %s: %w", rt.defaultImage, err)
	}
	return nil
}

// ClaudeAuth is the default image's claude with args, for the hub's own
// sign-in, refresh and logout on a hub with no claude of its own
// (ADR-0056). It runs in a throwaway container as the hub's own user, with
// configDir mounted as its config directory and no other mount, behind the
// same egress wall as a workstation, whose built-in hosts include the
// CLI's token endpoint. It shares the wall's own network with the proxy
// and login checks, never a loop network, and it listens on nothing: with
// no TTY, claude auth login takes the pasted code, and its redirect is
// platform.claude.com's own callback page, not a local port (checked on
// 2.1.303, #956). It holds no capability, gains no privilege, and
// writes nothing but that directory and a tmpfs /tmp. The container ends
// itself after lifetime, and remove removes it by name, since killing the
// client leaves it running (ADR-0018). The caller runs remove once cmd has
// ended or been killed.
func (rt *Runtime) ClaudeAuth(ctx context.Context, configDir string, lifetime time.Duration, args []string) (cmd *exec.Cmd, remove func(), err error) {
	if strings.Contains(configDir, ",") {
		return nil, nil, fmt.Errorf("docker: a sign-in directory with a comma can't be mounted: %s", configDir)
	}
	if err := rt.ensureEgress(ctx); err != nil {
		return nil, nil, err
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return nil, nil, err
	}
	name := "spool-sign-in-" + hex.EncodeToString(suffix)
	argv := []string{"run", "--pull=never", "--rm", "-i", "--name", name, "--label", signInLabel,
		"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp",
		"--mount", "type=bind,src=" + configDir + ",dst=" + signInConfigDir}
	if rt.egressEnabled() {
		argv = append(argv, "--network", rt.egressNetwork())
	}
	argv = append(argv, rt.egressEnv()...)
	argv = append(argv,
		"--env", "HOME="+auxHome,
		"--env", "CLAUDE_CONFIG_DIR="+signInConfigDir,
		"--env", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"--workdir", "/tmp", rt.defaultImage,
		"timeout", strconv.Itoa(int(lifetime.Seconds())), "claude")
	argv = append(argv, args...)
	cmd = exec.CommandContext(ctx, rt.bin, argv...)
	cmd.SysProcAttr = runtime.ChildAttr()
	remove = func() {
		_, _ = rt.command(context.Background(), queryTimeout, "rm", "-f", name)
	}
	return cmd, remove, nil
}
