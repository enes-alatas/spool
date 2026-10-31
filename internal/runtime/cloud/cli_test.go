package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		APIBase: environmentsListing(t, testEnvironment, "env_01Moved", "env_01Fixture"),
		Account: func(context.Context, string) (Account, error) { return testAccount, nil },
		Dir:     t.TempDir(),
		CLI:     &CLI{Binary: binary, Dir: filepath.Join(t.TempDir(), "cli"), Env: os.Environ(), StartWithin: 2 * time.Second},
		Bounds:  Bounds{Quiet: 5 * time.Second},
	}
}

// environmentsListing stands in for Anthropic's API, listing environments
// as the test account's on any token, and answers with its address: every
// start lists the account's environments first (#1232), and a runtime
// with no API base would ask Anthropic's own.
func environmentsListing(t *testing.T, environments ...string) string {
	t.Helper()
	var listed []string
	for _, environment := range environments {
		listed = append(listed, `{"kind":"anthropic_cloud","environment_id":"`+environment+`","name":"spool","state":"active"}`)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"environments":[` + strings.Join(listed, ",") + `]}`))
	}))
	t.Cleanup(api.Close)
	return api.URL
}

// startupHook is the input of a session's first SessionStart hook.
var startupHook = []byte(`{"hook_event_name":"SessionStart","source":"startup"}`)

// postStartup posts a session's first SessionStart for loopID, once the
// pending start's CLI, if there is one, has printed it created sessionID.
func postStartup(ctx context.Context, cloud *Runtime, loopID, sessionID string) error {
	printView(cloud, loopID, sessionID)
	_, err := cloud.SessionStart(ctx, loopID, sessionID, startupHook)
	return err
}

// printView stands in for the CLI's `View:` line naming sessionID, read
// for loopID's pending start, if there is one.
func printView(cloud *Runtime, loopID, sessionID string) {
	cloud.waits.mu.Lock()
	start := cloud.waits.starts[loopID]
	cloud.waits.mu.Unlock()
	if start != nil {
		cloud.waits.viewed(start, sessionAddress(sessionID))
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
			if postStartup(context.Background(), cloud, "loop-1", sessionID) == nil {
				break
			}
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", sessionID, post.Hook, post.Lines, false) == nil {
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
  git remote get-url origin; git symbolic-ref HEAD; ls -A | tr "\n" " "; echo
  git rev-list --all | wc -l
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
	denied, _ := json.Marshal(deniedTools())
	settings := `{"permissions":{"deny":` + string(denied) + `},"remote":{"defaultEnvironmentId":"` + testEnvironment + `"}}`
	for _, want := range []string{
		"terminal\n",
		"args: --cloud first words --permission-mode auto --settings " + settings + " --ref main --debug-file ",
		"token: " + testAccount.Token + "\n",
		`"hasCompletedOnboarding":true`,
		`"organizationUuid":"` + testAccount.OrgUUID + `"`,
		`"hasTrustDialogAccepted":true`,
		"https://github.com/example-org/cirrus.git\nrefs/heads/main\n.git \n0\n",
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
			t.Errorf("%s outlived the start (%v): the start directory and the CLI's state go with it", dir, err)
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

// CLI 2.1.310 ends once it has created the session, before the VM posts
// SessionStart: a clean exit leaves the start waiting for that post (#1102).
func TestCLIStartThatExitsCleanlyTakesTheLaterPost(t *testing.T) {
	exited := filepath.Join(t.TempDir(), "exited")
	cloud := cliRuntime(t, "touch "+exited+"; exit 0")
	proc := startCLI(t, cloud, testSpec())
	postWhenPending(t, cloud, exited, "session_01Synthetic", StopPost{
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
}

// A start with no environment fails before the CLI runs: the CLI would
// pick one of the account's environments by itself (ADR-0061).
func TestCLIStartNeedsTheLoopsEnvironment(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	cloud := cliRuntime(t, "touch "+ran)
	spec := testSpec()
	spec.Cloud.EnvironmentID = ""
	proc, err := cloud.Start(context.Background(), spec)
	if err == nil {
		_ = proc.Kill()
		t.Fatal("a loop with no environment started")
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the CLI ran for a loop with no environment")
	}
}

// A start whose session posts nothing within its bound fails, naming the
// bound and the CLI's last line. When the CLI exited cleanly, so the
// session was created, it is told to Unreached: the session can't reach
// the hub. One whose session posted, even a post the runtime refused,
// reached it, and is not; nor is one whose CLI still runs, which has
// created no session yet (#1093, #1102).
func TestCLIStartFailsWhenNoSessionStartsInTime(t *testing.T) {
	for name, tc := range map[string]struct {
		cli       string
		post      bool
		said      string
		unreached bool
	}{
		"nothing posted":         {cli: "exit 0", post: false, said: "the session never reached the hub within 300ms", unreached: true},
		"a post was refused":     {cli: "exit 0", post: true, said: "no session started within 300ms", unreached: false},
		"the CLI is still going": {cli: "sleep 30", post: false, said: "no session started within 300ms", unreached: false},
	} {
		t.Run(name, func(t *testing.T) {
			cloud := cliRuntime(t, `echo "Uploading the repository"; `+tc.cli)
			cloud.CLI.StartWithin = 300 * time.Millisecond
			var told []string
			var mu sync.Mutex
			cloud.Unreached = func(loopID string, within time.Duration) {
				mu.Lock()
				defer mu.Unlock()
				told = append(told, fmt.Sprintf("%s %s", loopID, within))
			}
			proc := startCLI(t, cloud, testSpec())
			if tc.post {
				go func() {
					resume := []byte(`{"hook_event_name":"SessionStart","source":"resume"}`)
					for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
						if cloud.waits.pending("loop-1") {
							_, _ = cloud.SessionStart(context.Background(), "loop-1", "session_01Synthetic", resume)
							return
						}
					}
				}()
			}
			_, result := turnResult(t, proc, "hello")
			if !result.IsError || !strings.Contains(result.ResultText, tc.said) ||
				!strings.Contains(result.ResultText, "Uploading the repository") {
				t.Errorf("the turn ended as %q, want an error saying %q and the CLI's last line", result.ResultText, tc.said)
			}
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"loop-1 300ms"}; tc.unreached != (len(told) == 1 && told[0] == want[0]) {
				t.Errorf("Unreached was told %q, want it told: %v", told, tc.unreached)
			}
			if err := postStartup(context.Background(), cloud, "loop-1", "session_01Late"); err == nil {
				t.Error("a SessionStart after the start failed was taken")
			}
		})
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
	// the VM names its session cse_…, and the send names it session_…
	postWhenPending(t, cloud, "", "cse_01Synthetic", StopPost{Hook: []byte(`{}`), Lines: []byte(transcriptTurn)})
	turnResult(t, proc, "first words")

	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", "session_01Synthetic", []byte(`{"last_assistant_message":"second"}`), nil, false) == nil {
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
			if postStartup(context.Background(), cloud, "loop-1", "session_01Synthetic") == nil {
				break
			}
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", "session_01Synthetic", []byte(`{}`), nil, true) == nil {
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
	if err := postStartup(ctx, cloud, "loop-1", "session_01Synthetic"); err == nil {
		t.Error("a SessionStart with no start pending was taken")
	}
	if err := cloud.Stop(ctx, "loop-1", "session_01Synthetic", []byte(`{}`), nil, false); err == nil {
		t.Error("a Stop with no turn in flight was taken")
	}

	start := cloud.waits.awaitStart("loop-1", time.Minute, nil)
	if err := postStartup(ctx, cloud, "loop-2", "session_01Synthetic"); err == nil {
		t.Error("another loop's post was taken for this loop's start")
	}
	if err := postStartup(ctx, cloud, "loop-1", "session_01First"); err != nil {
		t.Fatal(err)
	}
	if err := postStartup(ctx, cloud, "loop-1", "session_01Second"); err == nil {
		t.Error("a second SessionStart for one start was taken")
	}
	if got := <-start.session; got != "session_01First" {
		t.Errorf("the start got %q, want the first post's", got)
	}

	cloud.waits.awaitStart("loop-1", -time.Second, nil)
	if err := postStartup(ctx, cloud, "loop-1", "session_01Late"); err == nil {
		t.Error("a SessionStart past its start's bound was taken")
	}
}

// A start takes only a post from the session its CLI printed it created
// (ADR-0061): an old session of the loop may hold a token that works, and
// one posting inside the start's window is refused.
func TestCLIStartTakesOnlyTheSessionItsCLIPrinted(t *testing.T) {
	cloud := cliRuntime(t, `printf 'Created cloud session: Greeting\r\n\033[2mView: https://claude.ai/code/session_01Printed?from=cli&m=0\033[0m\r\nResume with: claude --teleport session_01Printed\r\n'
sleep 30
`)
	proc := startCLI(t, cloud, testSpec())
	stale := make(chan error, 1)
	go func() {
		ctx := context.Background()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			_, err := cloud.SessionStart(ctx, "loop-1", "session_01Stale", startupHook)
			if !errors.Is(err, errNoHandOff) {
				stale <- err
				break
			}
		}
		// the VM names its session cse_…
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := cloud.SessionStart(ctx, "loop-1", "cse_01Printed", startupHook); err == nil {
				break
			}
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(ctx, "loop-1", "cse_01Printed", []byte(`{}`), []byte(transcriptTurn), false) == nil {
				return
			}
		}
	}()

	_, result := turnResult(t, proc, "first words")
	if result.IsError {
		t.Fatalf("the turn's result: %+v", result)
	}
	if err := <-stale; !errors.Is(err, errOtherSession) {
		t.Errorf("the other session's post = %v, want it refused as another session", err)
	}
	if url := cloud.SessionURL("loop-1"); url != "https://claude.ai/code/session_01Printed" {
		t.Errorf("the loop's session link = %q, want the printed session's", url)
	}
}

// A post can reach the hub before the CLI's line has come through the
// terminal, and the hook doesn't post again: it waits for the line within
// the start's bound, and is judged then.
func TestASessionStartPostWaitsForTheCLIsLine(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	ctx := context.Background()
	start := cloud.waits.awaitStart("loop-1", time.Minute, []byte("export A='1'\n"))
	answered := make(chan error, 1)
	go func() {
		_, err := cloud.SessionStart(ctx, "loop-1", "session_01Early", startupHook)
		answered <- err
	}()
	select {
	case err := <-answered:
		t.Fatalf("the post was judged before the line was read: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cloud.waits.viewed(start, "session_01Early")
	if err := <-answered; err != nil {
		t.Fatalf("the held post, once the line named it: %v", err)
	}
	if got := <-start.session; got != "session_01Early" {
		t.Errorf("the start took %q", got)
	}

	cloud.waits.awaitStart("loop-1", 200*time.Millisecond, nil)
	if _, err := cloud.SessionStart(ctx, "loop-1", "session_01Unprinted", startupHook); !errors.Is(err, errNoHandOff) {
		t.Errorf("a post whose start's CLI printed nothing in its bound = %v, want no hand-off", err)
	}
}

func TestViewedSessionReadsTheCLIsLine(t *testing.T) {
	for line, want := range map[string]string{
		"View: https://claude.ai/code/session_01MWPu?from=cli&m=0": "session_01MWPu",
		"View: https://claude.ai/code/session_01MWPu":              "session_01MWPu",
		"View: https://example.test/code/session_01MWPu":           "",
		"Resume with: claude --teleport session_01MWPu":            "",
		"View: https://claude.ai/code/session_01MWPu and more":     "",
		"Task: View: https://claude.ai/code/session_01MWPu":        "",
	} {
		if got, _ := viewedSession(line); got != want {
			t.Errorf("viewedSession(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestLastLineDropsTheTerminalsEscapes(t *testing.T) {
	screen := &lastLine{}
	_, _ = screen.Write([]byte("\x1b[2J\x1b[1;1HWelcome\r\n\x1b[32m✓ Uploaded\x1b[0m\r\n\x1b[?25l"))
	if got := screen.String(); got != "✓ Uploaded" {
		t.Errorf("the last line is %q", got)
	}
	// the escapes CLI 2.1.310 left in a start error's last line (#1102)
	if got := stripEscapes([]byte("\x1b(B\x1b7\x1b8\x1b]0;claude\x07Started\x1b]2;title\x1b\\ \x1bMhere")); got != "Started here" {
		t.Errorf("stripEscapes left %q", got)
	}
}

// A hub that stopped mid-start left that start's directory in the CLI's
// dir: the next boot removes it. A dir at that path that
// isn't this user's own is refused, at boot and before every run.
func TestPrepareSweepsTheCLIsDirAndRefusesOneNotPrivate(t *testing.T) {
	cli := &CLI{Dir: filepath.Join(t.TempDir(), "cli")}
	if removed, err := cli.Prepare(); err != nil || removed != 0 {
		t.Fatalf("Prepare on no dir = %d, %v; want it made, with nothing removed", removed, err)
	}
	if err := os.MkdirAll(filepath.Join(cli.Dir, "spool-start-1", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cli.Dir, "spool-start-1", ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if removed, err := cli.Prepare(); err != nil || removed != 1 {
		t.Fatalf("Prepare = %d, %v; want the left directory removed", removed, err)
	}
	if entries, _ := os.ReadDir(cli.Dir); len(entries) != 0 {
		t.Errorf("after Prepare the dir holds %v", entries)
	}

	if err := os.Chmod(cli.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Prepare(); err == nil {
		t.Error("Prepare took a dir other users can open")
	}
	if err := cli.ownDir(); err == nil {
		t.Error("a run took a dir other users can open")
	}
	link := &CLI{Dir: filepath.Join(t.TempDir(), "link")}
	if err := os.Symlink(t.TempDir(), link.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := link.Prepare(); err == nil {
		t.Error("Prepare followed a symlink")
	}
}

// What the CLI writes into its config dir besides what the hub gave it is
// removed with the dir, and named; a clean run leaves nothing to name.
func TestARunsConfigDirIsCheckedAndRemoved(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	clean, err := cloud.newCLIRun(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	configDir := configDirOf(clean)
	if found := leftovers(configDir); len(found) != 0 {
		t.Errorf("a clean run's config dir holds %v", found)
	}
	clean.remove()

	dirty, err := cloud.newCLIRun(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	configDir = configDirOf(dirty)
	for _, name := range []string{"projects/-tmp/s.jsonl", "shell-snapshots/s.sh", "debug/s.txt", ".credentials.json"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(configDir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, name), []byte("[wake] synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if found := leftovers(configDir); strings.Join(found, " ") != ".credentials.json projects shell-snapshots debug" {
		t.Errorf("the leftovers named are %v", found)
	}
	dirty.remove()
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("the config dir outlived its run: %v", err)
	}
	if entries, _ := os.ReadDir(cloud.CLI.Dir); len(entries) != 0 {
		t.Errorf("the runs left %v in the CLI's dir", entries)
	}
}

// A send runs in an empty dir of its own, never the hub's working dir,
// whose project settings and hooks the CLI would load.
func TestASendRunsInAnEmptyDirOfItsOwn(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	cloud := cliRuntime(t, `case "$1" in
--cloud) sleep 30 ;;
-p) { pwd; ls -A; } > `+seen+`; echo '{"ok":true}' ;;
esac
`)
	proc := startCLI(t, cloud, testSpec())
	postWhenPending(t, cloud, "", "session_01Synthetic", StopPost{Hook: []byte(`{}`), Lines: []byte(transcriptTurn)})
	turnResult(t, proc, "first words")
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", "session_01Synthetic", []byte(`{}`), nil, false) == nil {
				return
			}
		}
	}()
	turnResult(t, proc, "second words")
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	hubDir, _ := os.Getwd()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], cloud.CLI.Dir+"/") || lines[0] == hubDir {
		t.Errorf("the send ran in %q, want an empty dir under the CLI's dir", lines)
	}
}

func TestCheckAccountRunsTheLoginCheckOnTheAccountsToken(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	cloud := cliRuntime(t, `echo "$* $CLAUDE_CODE_OAUTH_TOKEN" > `+seen+`
read -r line
echo '{"type":"result","subtype":"success","is_error":false,"result":"hi"}'
`)
	check, err := cloud.CheckAccount(context.Background())
	if err != nil || !check.OK {
		t.Fatalf("CheckAccount = %+v, %v; want a passed check", check, err)
	}
	raw, _ := os.ReadFile(seen)
	if want := strings.Join(claude.LoginCheckArgs(), " ") + " " + testAccount.Token + "\n"; string(raw) != want {
		t.Errorf("the check ran as %q, want %q", raw, want)
	}

	cloud.Account = func(context.Context, string) (Account, error) { return Account{}, ErrNoAccount }
	if _, err := cloud.CheckAccount(context.Background()); !errors.Is(err, ErrNoAccount) {
		t.Errorf("with no account CheckAccount = %v, want ErrNoAccount", err)
	}
}

// configDirOf is the config dir run sets, the last CLAUDE_CONFIG_DIR in
// its environment.
func configDirOf(run cliRun) (dir string) {
	for _, kv := range run.env {
		if value, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			dir = value
		}
	}
	return dir
}

// A private dir another user owns is refused too: a hub running as root
// could write into it, and its owner could swap what the hub put there.
// /root stands in for it where it is a 0700 dir this user doesn't own.
func TestOwnDirRefusesAnotherUsersPrivateDir(t *testing.T) {
	info, err := os.Lstat("/root")
	if err != nil || info.Mode().Perm() != 0o700 || os.Getuid() == 0 {
		t.Skip("no private dir of another user's here")
	}
	cli := &CLI{Dir: "/root"}
	if err := cli.ownDir(); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Errorf("ownDir on another user's private dir = %v, want it refused", err)
	}
}

// pending reports whether a start of loopID waits for its SessionStart.
func (waits *hookWaits) pending(loopID string) bool {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	return waits.starts[loopID] != nil
}

// A start directory's git takes the repo and branch as arguments, so
// makeStartDir checks them itself, whatever its caller checked: a branch
// read as a flag, or a repo that isn't owner/name, makes no directory.
func TestStartDirRefusesARepoOrBranchGitCouldMisread(t *testing.T) {
	parent := t.TempDir()
	for _, source := range []runtime.CloudStart{
		{Repo: "example-org/cirrus", Branch: "--upload-pack=touch pwned", EnvironmentID: "env_01Fixture"},
		{Repo: "example-org/cirrus", Branch: "-b", EnvironmentID: "env_01Fixture"},
		{Repo: "example-org/../cirrus", Branch: "main", EnvironmentID: "env_01Fixture"},
		{Repo: "--config=x/y", Branch: "main", EnvironmentID: "env_01Fixture"},
	} {
		if dir, err := makeStartDir(context.Background(), parent, source); err == nil {
			t.Errorf("a start from %q on %q made %s, want it refused", source.Repo, source.Branch, dir)
		}
	}
	if left, _ := os.ReadDir(parent); len(left) != 0 {
		t.Errorf("refused starts left %d directories", len(left))
	}
	// edges the store's rules take, which the runtime mustn't refuse
	for _, source := range []runtime.CloudStart{
		{Repo: "example-org/cirrus", Branch: "feat/x", EnvironmentID: "env_01Fixture"},
		{Repo: "a-b/-dash.name_1", Branch: "v1.2@x", EnvironmentID: "env_01Fixture"},
	} {
		dir, err := makeStartDir(context.Background(), parent, source)
		if err != nil {
			t.Errorf("a start from %q on %q: %v", source.Repo, source.Branch, err)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			t.Errorf("the start directory holds no git repo: %v", err)
		}
	}
}

// A Stop post ends the turn only from the loop's recorded session, in
// either of its ID's forms (ADR-0061): every session in the loop's
// environment holds the token.
func TestCLIStopTakesOnlyTheLoopsSession(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	ctx := context.Background()
	if err := cloud.update("loop-1", func(current *state) { current.SessionID = "session_01Recorded" }); err != nil {
		t.Fatal(err)
	}
	stops := cloud.waits.awaitStop("loop-1")
	defer cloud.waits.forgetStop("loop-1", stops)
	for _, other := range []string{"session_01Other", "cse_01Other", ""} {
		if err := cloud.Stop(ctx, "loop-1", other, []byte(`{}`), nil, false); !errors.Is(err, errNotTheSession) {
			t.Errorf("a Stop post from %q: %v, want refused", other, err)
		}
	}
	if err := cloud.Stop(ctx, "loop-1", "cse_01Recorded", []byte(`{}`), nil, false); err != nil {
		t.Fatalf("the loop's own Stop post: %v", err)
	}
	if arrival := <-stops; string(arrival.post.Hook) != `{}` {
		t.Errorf("the turn got %+v", arrival)
	}
}

// /mcp takes a tool call only from the loop's session, and only while a turn
// of it runs or within turnGrace of its end (ADR-0061).
func TestCLIMCPCallTakesOnlyTheLoopsSessionInATurn(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	ctx := context.Background()
	if err := cloud.MCPCall(ctx, "loop-1", "session_01Recorded"); !errors.Is(err, errNotTheSession) {
		t.Errorf("a call before any session: %v, want refused", err)
	}
	if err := cloud.update("loop-1", func(current *state) { current.SessionID = "session_01Recorded" }); err != nil {
		t.Fatal(err)
	}
	if err := cloud.MCPCall(ctx, "loop-1", "session_01Recorded"); !errors.Is(err, errNoTurn) {
		t.Errorf("a call with no turn ever run: %v, want refused", err)
	}

	stops := cloud.waits.awaitStop("loop-1")
	if err := cloud.MCPCall(ctx, "loop-1", "cse_01Recorded"); err != nil {
		t.Errorf("the session's call in a turn: %v", err)
	}
	if err := cloud.MCPCall(ctx, "loop-1", "cse_01ByHand"); !errors.Is(err, errNotTheSession) {
		t.Errorf("another session's call in a turn: %v, want refused", err)
	}
	cloud.waits.forgetStop("loop-1", stops)
	if err := cloud.MCPCall(ctx, "loop-1", "session_01Recorded"); err != nil {
		t.Errorf("the session's call just after its turn: %v", err)
	}
	cloud.waits.mu.Lock()
	cloud.waits.ended["loop-1"] = time.Now().Add(-turnGrace)
	cloud.waits.mu.Unlock()
	if err := cloud.MCPCall(ctx, "loop-1", "session_01Recorded"); !errors.Is(err, errNoTurn) {
		t.Errorf("the session's call past the grace: %v, want refused", err)
	}
	// its client may still connect between turns, and only it
	if err := cloud.MCPSession(ctx, "loop-1", "cse_01Recorded"); err != nil {
		t.Errorf("the session's handshake between turns: %v", err)
	}
	if err := cloud.MCPSession(ctx, "loop-1", "cse_01ByHand"); !errors.Is(err, errNotTheSession) {
		t.Errorf("another session's handshake: %v, want refused", err)
	}

	// a turn a hub restart cut off may run until its bound
	if err := cloud.update("loop-1", func(current *state) {
		current.Turn = &turnState{Since: time.Now().UnixMilli(), Until: time.Now().Add(time.Minute).UnixMilli(), CutOff: true}
	}); err != nil {
		t.Fatal(err)
	}
	if err := cloud.MCPCall(ctx, "loop-1", "session_01Recorded"); err != nil {
		t.Errorf("the session's call in a cut-off turn: %v", err)
	}
}

// While a start is pending, /mcp takes the session the start's CLI
// printed, and a call before the line is read waits for it: the client
// connects before SessionStart binds the session (run S on #1180).
func TestCLIMCPCallWaitsForAPendingStartsSession(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	ctx := context.Background()
	if err := cloud.update("loop-1", func(current *state) { current.SessionID = "session_01Old" }); err != nil {
		t.Fatal(err)
	}
	start := cloud.waits.awaitStart("loop-1", time.Minute, nil)
	defer cloud.waits.forgetStart("loop-1", start)
	early := make(chan error, 1)
	go func() { early <- cloud.MCPCall(ctx, "loop-1", "cse_01Printed") }()
	select {
	case err := <-early:
		t.Fatalf("a call before the CLI's line was judged at once: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cloud.waits.viewed(start, "session_01Printed")
	if err := <-early; err != nil {
		t.Errorf("the printed session's early call: %v", err)
	}
	if err := cloud.MCPCall(ctx, "loop-1", "session_01Old"); !errors.Is(err, errNotTheSession) {
		t.Errorf("the loop's old session's call during a start: %v, want refused", err)
	}

	cloud.waits.forgetStart("loop-1", start)
	unread := cloud.waits.awaitStart("loop-1", 50*time.Millisecond, nil)
	defer cloud.waits.forgetStart("loop-1", unread)
	if err := cloud.MCPCall(ctx, "loop-1", "session_01Printed"); !errors.Is(err, errNotTheSession) {
		t.Errorf("a call when the start's line never came: %v, want refused", err)
	}
}
