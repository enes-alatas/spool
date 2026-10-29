package cloud

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The export lines are read back by sh as the values were: quotes,
// newlines, command substitutions and backticks stay text. A name outside
// ADR-0043's rule, or a value holding a NUL, is left out.
func TestExportLinesRoundTripThroughSh(t *testing.T) {
	values := map[string]string{
		"QUOTED":   `it's "quoted" \ and '' twice`,
		"LINES":    "first\nsecond\n",
		"COMMANDS": "$(touch pwned) `touch pwned` ${HOME} $HOME",
		"EMPTY":    "",
	}
	env := map[string]string{"BAD-NAME": "x", "WITH_NUL": "a\x00b"}
	for name, value := range values {
		env[name] = value
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "env")
	if err := os.WriteFile(file, exportLines(env), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range values {
		cmd := exec.Command("sh", "-c", `. ./env && printf '%s' "$`+name+`"`)
		cmd.Dir, cmd.Env = dir, []string{"PATH=" + os.Getenv("PATH")}
		got, err := cmd.Output()
		if err != nil || string(got) != want {
			t.Errorf("%s reads back as %q (%v), want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("reading the lines ran a command a value held")
	}
	if lines := string(exportLines(env)); strings.Contains(lines, "BAD-NAME") || strings.Contains(lines, "WITH_NUL") {
		t.Errorf("a name off ADR-0043's rule or a value with a NUL was handed over:\n%s", lines)
	}
}

// A start is answered with its exports, once, and logged without them. Any
// other post is refused, and a refused post from the session the loop
// recorded is told.
func TestSessionStartHandsTheConnectionsOverOnlyWhenAwaited(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	var refused, answered int
	cloud.HandOff = func(loopID string, ok bool) {
		if loopID != "loop-1" {
			t.Errorf("told of loop %q", loopID)
		}
		if ok {
			answered++
		} else {
			refused++
		}
	}
	ctx := context.Background()
	exports := []byte("export GH_TOKEN='synthetic'\n")

	cloud.waits.awaitStart("loop-1", time.Minute, exports)
	printView(cloud, "loop-1", "session_01First")
	if answer, err := cloud.SessionStart(ctx, "loop-1", "session_01First", startupHook); err != nil || string(answer) != string(exports) {
		t.Fatalf("the start was answered %q (%v), want the exports", answer, err)
	}
	if err := cloud.update("loop-1", func(loaded *state) { loaded.SessionID = "session_01First" }); err != nil {
		t.Fatal(err)
	}

	for name, hook := range map[string][]byte{
		"a resume":                    []byte(`{"source":"resume"}`),
		"a startup with no start":     startupHook,
		"a clear":                     []byte(`{"source":"clear"}`),
		"a compaction":                []byte(`{"source":"compact"}`),
		"a post that names no source": nil,
	} {
		if answer, err := cloud.SessionStart(ctx, "loop-1", "session_01First", hook); err == nil {
			t.Errorf("%s was answered %q", name, answer)
		}
	}
	if refused != 5 {
		t.Errorf("the recorded session's refused posts were told %d times, want 5", refused)
	}

	cloud.waits.awaitStart("loop-1", time.Minute, exports)
	cloud.waits.starts["loop-1"].deadline = time.Now().Add(-time.Second)
	if _, err := cloud.SessionStart(ctx, "loop-1", "session_01Other", startupHook); err == nil {
		t.Error("a start past its bound was answered")
	}
	if refused != 5 {
		t.Error("a refused post from a session the loop never recorded was told")
	}
	if answered != 1 {
		t.Errorf("answers were told %d times, want the start's", answered)
	}
	if !strings.Contains(logged.String(), "source=startup") {
		t.Errorf("the answered start wasn't logged:\n%s", logged.String())
	}
	if strings.Contains(logged.String(), "synthetic") {
		t.Error("a hand-off's log carries its values")
	}
}
