package cloud

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// cliRuntime is a cloud runtime driving its sessions through cli, a shell
// script standing in for the claude CLI.
func cliRuntime(t *testing.T, cli string) *Runtime {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+cli), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Runtime{
		Client: &Client{Account: func(context.Context) (Account, error) { return testAccount, nil }},
		Dir:    t.TempDir(),
		CLI:    &CLI{Binary: binary, Dir: t.TempDir(), Env: os.Environ(), StartWithin: 2 * time.Second},
		Bounds: Bounds{Quiet: 5 * time.Second},
	}
}

func startCLI(t *testing.T, cloud *Runtime, spec runtime.Spec) runtime.Proc {
	t.Helper()
	proc, err := cloud.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Kill(); proc.Wait() })
	return proc
}

// turnResult sends text and reads the turn's events to its result.
func turnResult(t *testing.T, proc runtime.Proc, text string) (events []claude.Event, result *claude.ResultInfo) {
	t.Helper()
	if err := proc.Send(text); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case event, ok := <-proc.Events():
			if !ok {
				t.Fatal("the Proc ended before the turn's result")
			}
			events = append(events, event)
			if event.Result != nil {
				return events, event.Result
			}
		case <-timeout:
			t.Fatal("no result")
		}
	}
}

// postWhenPending makes the hook posts of a session once its start is
// pending and the file after exists, when it isn't "": the session's id,
// then the turn.
func postWhenPending(t *testing.T, cloud *Runtime, after, sessionID string, post StopPost) {
	t.Helper()
	go func() {
		for deadline := time.Now().Add(5 * time.Second); after != "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(after); err == nil {
				break
			}
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.SessionStart(context.Background(), "loop-1", sessionID, nil) == nil {
				break
			}
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", post.Hook, post.Lines, false) == nil {
				return
			}
		}
	}()
}

func TestCLIStartRecordsTheSessionItsHookPosted(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	cloud := cliRuntime(t, `{
  [ -t 0 ] && echo terminal
  echo "args: $*"
  echo "token: $CLAUDE_CODE_OAUTH_TOKEN"
  cat "$CLAUDE_CONFIG_DIR/.claude.json"; echo
  echo "home: $(pwd)"
  cat CLAUDE.md; echo
  git log --oneline | wc -l
  echo "$CLAUDE_CONFIG_DIR"
} > `+seen+`.tmp
mv `+seen+`.tmp `+seen+`
sleep 30
`)
	proc := startCLI(t, cloud, testSpec())
	// the VM posts once the CLI has uploaded: here, once it has written
	postWhenPending(t, cloud, seen, "session_01Synthetic", StopPost{
		Hook:  []byte(`{"last_assistant_message":"It is Tuesday."}`),
		Lines: []byte(transcriptTurn),
	})

	_, result := turnResult(t, proc, "first words")
	if result.IsError || result.ResultText != "It is Tuesday." {
		t.Fatalf("the turn's result: %+v", result)
	}
	if url := cloud.SessionURL("loop-1"); url != "https://claude.ai/code/session_01Synthetic" {
		t.Errorf("the loop's session link = %q, want the posted session's", url)
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"terminal\n",
		"args: --cloud first words --permission-mode auto\n",
		"token: " + testAccount.Token + "\n",
		`"hasCompletedOnboarding":true`,
		`"organizationUuid":"` + testAccount.OrgUUID + `"`,
		`"hasTrustDialogAccepted":true`,
		"You are cirrus.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the CLI ran without %q:\n%s", want, got)
		}
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	configDir := lines[len(lines)-1]
	var home string
	for _, line := range lines {
		if after, ok := strings.CutPrefix(line, "home: "); ok {
			home = after
		}
	}
	for _, dir := range []string{home, configDir} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s outlived the start (%v): it holds the loop's token or the CLI's state", dir, err)
		}
	}
}

func TestCLIStartFailsWithTheCLIsLastLine(t *testing.T) {
	cloud := cliRuntime(t, `printf '\033[31mError: no session for you\033[0m\r\n'; exit 1`)
	proc := startCLI(t, cloud, testSpec())
	_, result := turnResult(t, proc, "hello")
	if !result.IsError || !strings.Contains(result.ResultText, "exited") || !strings.Contains(result.ResultText, "Error: no session for you") {
		t.Errorf("the turn ended as %q, want an error quoting the CLI's last line", result.ResultText)
	}
}

func TestCLIStartFailsWhenNoSessionStartsInTime(t *testing.T) {
	cloud := cliRuntime(t, `echo "Uploading the repository"; sleep 30`)
	cloud.CLI.StartWithin = 300 * time.Millisecond
	proc := startCLI(t, cloud, testSpec())
	_, result := turnResult(t, proc, "hello")
	if !result.IsError || !strings.Contains(result.ResultText, "no session started within 300ms") ||
		!strings.Contains(result.ResultText, "Uploading the repository") {
		t.Errorf("the turn ended as %q, want an error naming the bound and the CLI's last line", result.ResultText)
	}
	if err := cloud.SessionStart(context.Background(), "loop-1", "session_01Late", nil); err == nil {
		t.Error("a SessionStart after the start failed was taken")
	}
}

func TestCLISendsIntoTheSessionAndFailsOnARefusal(t *testing.T) {
	answer := filepath.Join(t.TempDir(), "answer")
	cloud := cliRuntime(t, `case "$1" in
--cloud) sleep 30 ;;
-p) echo "$*" >> `+answer+`.args; cat `+answer+`; [ "$(cat `+answer+`)" = '{"ok":true}' ] ;;
esac
`)
	if err := os.WriteFile(answer, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	proc := startCLI(t, cloud, testSpec())
	postWhenPending(t, cloud, "", "session_01Synthetic", StopPost{Hook: []byte(`{}`), Lines: []byte(transcriptTurn)})
	turnResult(t, proc, "first words")

	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", []byte(`{"last_assistant_message":"second"}`), nil, false) == nil {
				return
			}
		}
	}()
	if _, result := turnResult(t, proc, "second words"); result.IsError || result.ResultText != "second" {
		t.Fatalf("the sent turn: %+v", result)
	}
	args, _ := os.ReadFile(answer + ".args")
	if string(args) != "-p second words --cloud session_01Synthetic --output-format json\n" {
		t.Errorf("the send ran as %q", args)
	}

	if err := os.WriteFile(answer, []byte(`{"ok":false,"error":"the session is archived"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, result := turnResult(t, proc, "third words"); !result.IsError || !strings.Contains(result.ResultText, "the session is archived") {
		t.Errorf("a refused send ended as %q, want an error quoting the CLI", result.ResultText)
	}
}

func TestCLITurnWhosePostIsOverItsBoundEndsAsAnError(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	proc := startCLI(t, cloud, testSpec())
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.SessionStart(context.Background(), "loop-1", "session_01Synthetic", nil) == nil {
				break
			}
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", []byte(`{}`), nil, true) == nil {
				return
			}
		}
	}()
	if _, result := turnResult(t, proc, "hello"); !result.IsError || !strings.Contains(result.ResultText, "over its bound") {
		t.Errorf("the turn ended as %q, want an error naming the bound", result.ResultText)
	}
}

// The hooks' posts are taken only while the runtime waits for them: a
// start's session id once, and a turn's Stop once.
func TestCLIHookPostsAreTakenOnlyWhenAwaited(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	ctx := context.Background()
	if err := cloud.SessionStart(ctx, "loop-1", "session_01Synthetic", nil); err == nil {
		t.Error("a SessionStart with no start pending was taken")
	}
	if err := cloud.Stop(ctx, "loop-1", []byte(`{}`), nil, false); err == nil {
		t.Error("a Stop with no turn in flight was taken")
	}

	start := cloud.waits.awaitStart("loop-1", time.Minute)
	if err := cloud.SessionStart(ctx, "loop-2", "session_01Synthetic", nil); err == nil {
		t.Error("another loop's post was taken for this loop's start")
	}
	if err := cloud.SessionStart(ctx, "loop-1", "session_01First", nil); err != nil {
		t.Fatal(err)
	}
	if err := cloud.SessionStart(ctx, "loop-1", "session_01Second", nil); err == nil {
		t.Error("a second SessionStart for one start was taken")
	}
	if got := <-start.session; got != "session_01First" {
		t.Errorf("the start got %q, want the first post's", got)
	}

	cloud.waits.awaitStart("loop-1", -time.Second)
	if err := cloud.SessionStart(ctx, "loop-1", "session_01Late", nil); err == nil {
		t.Error("a SessionStart past its start's bound was taken")
	}
}

func TestLastLineDropsTheTerminalsEscapes(t *testing.T) {
	screen := &lastLine{}
	_, _ = screen.Write([]byte("\x1b[2J\x1b[1;1HWelcome\r\n\x1b[32m✓ Uploaded\x1b[0m\r\n\x1b[?25l"))
	if got := screen.String(); got != "✓ Uploaded" {
		t.Errorf("the last line is %q", got)
	}
}
