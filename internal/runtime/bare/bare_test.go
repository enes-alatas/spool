package bare

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/runtime"
)

// stubClaude writes a shell script standing in for the claude binary: it
// touches ready once its signal handler is installed, records a SIGTERM in
// signalled, and otherwise sits still.
func stubClaude(t *testing.T, ready, signalled string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ntrap 'echo term > " + signalled + "; exit 0' TERM\n" +
		"echo ready > " + ready + "\nsleep 30 &\nwait\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// waitFor blocks until path exists, failing the test if it never does.
func waitFor(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if exists(t, path) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// Start's ctx governs the spawn attempt only. Cancelling it must not reach
// the process — the wake path hands in a context that outlives the turn, and
// a kill-on-cancel watcher would leak a goroutine per wake.
func TestStartIgnoresContextCancellation(t *testing.T) {
	dir := t.TempDir()
	ready, signalled := filepath.Join(dir, "ready"), filepath.Join(dir, "signalled")
	host := New(stubClaude(t, ready, signalled))

	ctx, cancel := context.WithCancel(context.Background())
	proc, err := host.Start(ctx, runtime.Spec{WorkDir: t.TempDir(), SessionID: "11111111-1111-1111-1111-111111111111"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Kill() })

	waitFor(t, ready)
	cancel()
	time.Sleep(250 * time.Millisecond)
	if exists(t, signalled) {
		t.Fatal("context cancellation killed the process; Start must not watch ctx")
	}

	// Teardown still works — it goes through Kill, not the context.
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	proc.Wait()
	if !exists(t, signalled) {
		t.Fatal("Kill did not signal the process")
	}
}

// A resolution run gets an environment built from nothing (ADR-0033): no
// credential the hub holds reaches it, its config dir is empty and its own,
// and the only API it is told of is on loopback. It is ended at init.
func TestResolveModelRunHoldsNoCredential(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-oauth-token")
	t.Setenv("ANTHROPIC_API_KEY", "fixture-api-key")
	t.Setenv("GH_TOKEN", "fixture-gh-token")

	dir := t.TempDir()
	envDump, pidFile := filepath.Join(dir, "env"), filepath.Join(dir, "pid")
	stub := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nenv > " + envDump + "\necho $$ > " + pidFile + "\nread line\n" +
		`echo '{"type":"system","subtype":"init","session_id":"s","model":"claude-opus-5-5"}'` + "\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resolved, err := New(stub).ResolveModel(ctx, "opus")
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if resolved != "claude-opus-5-5" {
		t.Fatalf("resolved = %q, want what init reported", resolved)
	}

	raw, err := os.ReadFile(envDump)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	for _, leaked := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN"} {
		if _, ok := env[leaked]; ok {
			t.Errorf("%s reached the resolution run", leaked)
		}
	}
	if key := env["ANTHROPIC_API_KEY"]; strings.Contains(key, "fixture") {
		t.Errorf("the hub's ANTHROPIC_API_KEY reached the run")
	}
	if !strings.HasPrefix(env["ANTHROPIC_BASE_URL"], "http://127.0.0.1:") {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want a loopback port", env["ANTHROPIC_BASE_URL"])
	}
	if cfg := env["CLAUDE_CONFIG_DIR"]; cfg == "" || !strings.HasPrefix(cfg, env["HOME"]) || env["HOME"] == os.Getenv("HOME") {
		t.Errorf("HOME %q, CLAUDE_CONFIG_DIR %q: want a throwaway home holding the config dir", env["HOME"], cfg)
	}

	pid, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/proc/" + strings.TrimSpace(string(pid))); err == nil {
		t.Errorf("the run outlived its init")
	}
}

// A bare session's trace export is the hub's setting alone: every OTEL_*
// the hub inherited is dropped, and the loop's own variables still win
// (ADR-0064).
func TestEnvironDropsInheritedOTEL(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://elsewhere.example")
	t.Setenv("OTEL_LOG_USER_PROMPTS", "1")
	t.Setenv("SPOOL_TEST_INHERITED", "kept")
	env := environ(map[string]string{"OTEL_TRACES_EXPORTER": "otlp"})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, unwanted := range []string{"\nOTEL_EXPORTER_OTLP_ENDPOINT=", "\nOTEL_LOG_USER_PROMPTS="} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("an inherited %s reaches the session", strings.TrimSpace(unwanted))
		}
	}
	for _, wanted := range []string{"\nSPOOL_TEST_INHERITED=kept\n", "\nOTEL_TRACES_EXPORTER=otlp\n"} {
		if !strings.Contains(joined, wanted) {
			t.Errorf("the session lacks %s", strings.TrimSpace(wanted))
		}
	}
	if env := environ(nil); slices.ContainsFunc(env, func(variable string) bool { return strings.HasPrefix(variable, "OTEL_") }) {
		t.Error("a loop with no variables of its own still inherits OTEL_*")
	}
}

// The login check runs the host's claude too, so the hub's inherited
// OTEL_* stays out of it as well, and it exports nothing (ADR-0064).
func TestCheckLoginDropsInheritedOTEL(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://elsewhere.example")
	t.Setenv("CLAUDE_CODE_ENABLE_TELEMETRY", "1")
	dumped := filepath.Join(t.TempDir(), "env")
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nenv > "+dumped+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _ = New(bin).CheckLogin(context.Background(), "")
	env, err := os.ReadFile(dumped)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), "OTEL_") || !strings.Contains("\n"+string(env), "\nCLAUDE_CODE_ENABLE_TELEMETRY=0\n") {
		t.Errorf("the login check's env:\n%s", env)
	}
}
