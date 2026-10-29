package cloud

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// A synthetic account: never a real one.
var testAccount = Account{Token: "sk-ant-oat01-synthetic", OrgUUID: "00000000-0000-4000-8000-000000000001"}

// testEnvironment is the synthetic environment test sessions start in.
const testEnvironment = "env_01Synthetic"

// testSource is the synthetic repo, branch and environment test sessions
// start from.
var testSource = runtime.CloudStart{Repo: "example-org/cirrus", Branch: "main", EnvironmentID: testEnvironment}

func testSpec() runtime.Spec {
	return runtime.Spec{
		LoopID:             "loop-1",
		LoopName:           "cirrus",
		Model:              "claude-haiku-5-5",
		AppendSystemPrompt: "You are cirrus.",
		SessionID:          "hub-session-1",
		Cloud:              testSource,
	}
}

// resumeSpec is testSpec resuming its hub session, from source.
func resumeSpec(source runtime.CloudStart) runtime.Spec {
	spec := testSpec()
	spec.SessionID, spec.ResumeID = "", "hub-session-1"
	spec.Cloud = source
	return spec
}

func TestEnsureRefusesALoopWithNoSource(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	for _, unset := range []func(*runtime.CloudStart){
		func(source *runtime.CloudStart) { source.Repo = "" },
		func(source *runtime.CloudStart) { source.Branch = "" },
		func(source *runtime.CloudStart) { source.EnvironmentID = "" },
	} {
		spec := testSpec()
		unset(&spec.Cloud)
		if err := cloud.Ensure(context.Background(), spec); err == nil {
			t.Errorf("a loop with %+v passed Ensure", spec.Cloud)
		}
	}
	if err := cloud.Ensure(context.Background(), testSpec()); err != nil {
		t.Errorf("Ensure = %v, want nil for a loop with its source set", err)
	}
}

// startedSession runs testSpec's first turn to its end, so the runtime
// records session_01Synthetic as the loop's, started from testSource.
func startedSession(t *testing.T, cloud *Runtime) {
	t.Helper()
	proc := startCLI(t, cloud, testSpec())
	postWhenPending(t, cloud, "", "session_01Synthetic", StopPost{Hook: []byte(`{}`), Lines: []byte(transcriptTurn)})
	if _, result := turnResult(t, proc, "first words"); result.IsError {
		t.Fatalf("the first turn: %+v", result)
	}
	_ = proc.CloseStdin()
	proc.Wait()
}

// waitLost waits for proc to end as the CLI's session-not-found, which
// the runner answers with a fresh session.
func waitLost(t *testing.T, proc runtime.Proc) {
	t.Helper()
	lost := make(chan bool, 1)
	go func() { lost <- claude.IsSessionNotFound(proc.Wait()) }()
	select {
	case ok := <-lost:
		if !ok {
			t.Fatal("the resume ended, but not as the CLI's session-not-found")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the resume carried on in the session")
	}
}

func TestAResumedSessionSendsIntoTheSessionItRanIn(t *testing.T) {
	args := t.TempDir() + "/args"
	cloud := cliRuntime(t, `case "$1" in
--cloud) sleep 30 ;;
-p) echo "$*" >> `+args+`; echo '{"ok":true}' ;;
esac
`)
	startedSession(t, cloud)

	proc := startCLI(t, cloud, resumeSpec(testSource))
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", []byte(`{"last_assistant_message":"again"}`), nil, false) == nil {
				return
			}
		}
	}()
	if _, result := turnResult(t, proc, "second words"); result.IsError || result.ResultText != "again" {
		t.Fatalf("the resumed turn: %+v", result)
	}
	if sent, _ := os.ReadFile(args); string(sent) != "-p second words --cloud session_01Synthetic --output-format json\n" {
		t.Errorf("the resume sent %q, want a send into the session it ran in", sent)
	}
}

func TestAResumeOfAnUnknownSessionIsLost(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	spec := testSpec()
	spec.SessionID, spec.ResumeID = "", "hub-session-unknown"
	waitLost(t, startCLI(t, cloud, spec))
}

// A session started from another repo, branch or environment than the
// loop's now isn't resumed: the loop's next wake starts one of its own.
func TestAResumeFromAnotherSourceIsLost(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	startedSession(t, cloud)
	moved := testSource
	moved.EnvironmentID = "env_01Moved"
	waitLost(t, startCLI(t, cloud, resumeSpec(moved)))
}

// A session the sessions client created runs in an environment of the
// loop's own, so the loop's next wake starts a session through the CLI.
func TestAResumeOfASessionsClientSessionIsLost(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	if err := cloud.update("loop-1", func(s *state) {
		s.SessionID, s.ClaudeSession, s.Source = "session_01Old", "hub-session-1", sourceKey(testSource)
		s.EnvironmentID = "env_01Old"
	}); err != nil {
		t.Fatal(err)
	}
	waitLost(t, startCLI(t, cloud, resumeSpec(testSource)))
}

func TestDestroyForgetsTheSession(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	startedSession(t, cloud)
	if err := cloud.Destroy(context.Background(), "loop-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cloud.path("loop-1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the state file outlived the loop: %v", err)
	}
	if url := cloud.SessionURL("loop-1"); url != "" {
		t.Errorf("the destroyed loop still links %q", url)
	}
}

func TestFilesAreUnsupported(t *testing.T) {
	cloud := &Runtime{Dir: t.TempDir()}
	if err := cloud.PutFile(context.Background(), "loop-1", "/etc/passwd", "x"); !errors.Is(err, runtime.ErrUnsupported) {
		t.Errorf("PutFile = %v", err)
	}
	if _, err := cloud.GetFile(context.Background(), "loop-1", "/", "/etc/passwd", 1<<20); !errors.Is(err, runtime.ErrUnsupported) {
		t.Errorf("GetFile = %v", err)
	}
}

func TestSessionURLIsOnlyAClaudeAISessionPage(t *testing.T) {
	cloud := &Runtime{Dir: t.TempDir()}
	if got := cloud.SessionURL("loop_a"); got != "" {
		t.Errorf("with no session: %q, want none", got)
	}
	for url, want := range map[string]string{
		"https://claude.ai/code/session_01": "https://claude.ai/code/session_01",
		"https://claude.ai.example/code/x":  "",
		"javascript:alert(1)":               "",
		"http://claude.ai/code/session_01":  "",
	} {
		if err := cloud.update("loop_a", func(s *state) { s.SessionURL = url }); err != nil {
			t.Fatal(err)
		}
		if got := cloud.SessionURL("loop_a"); got != want {
			t.Errorf("session_url %q is served as %q, want %q", url, got, want)
		}
	}
}
