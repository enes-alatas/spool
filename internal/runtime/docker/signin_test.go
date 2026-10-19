package docker

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The hub's own claude auth runs as the hub's user, behind the egress
// wall, with no capability and a read-only root (#952). It sees only the
// sign-in's directory, takes no token from the environment, and ends
// itself.
func TestClaudeAuthSeesOnlyTheSignInDirectory(t *testing.T) {
	bin, _ := wallDocker(t, "")
	rt := New(Options{Bin: bin, DefaultImage: "spool-workstation", EgressImage: "spool-egress"})
	configDir := t.TempDir()
	cmd, remove, err := rt.ClaudeAuth(context.Background(), configDir, 90*time.Second, []string{"auth", "login", "--claudeai"})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	joined := strings.Join(cmd.Args[1:], " ")
	for _, want := range []string{
		"run --pull=never --rm -i ",
		"--user " + strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"--cap-drop ALL --security-opt no-new-privileges --read-only --tmpfs /tmp",
		"--mount type=bind,src=" + configDir + ",dst=" + signInConfigDir,
		"--network " + rt.egressNetwork(),
		"--env HTTPS_PROXY=" + rt.egressProxyURL(),
		"--env CLAUDE_CONFIG_DIR=" + signInConfigDir,
		"--env CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"spool-workstation timeout 90 claude auth login --claudeai",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %q: %s", want, joined)
		}
	}
	for _, unwanted := range []string{"--volume", "--add-host", "docker.sock", "TOKEN", "--network host"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("argv carries %q: %s", unwanted, joined)
		}
	}
	if strings.Count(joined, "--mount") != 1 {
		t.Errorf("argv mounts more than the sign-in's directory: %s", joined)
	}
	if _, _, err := rt.ClaudeAuth(context.Background(), "/data,dir", time.Minute, nil); err == nil {
		t.Error("a directory with a comma was mounted")
	}
}
