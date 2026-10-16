package cloud

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/runtime/cloud/cloudfake"
)

// A synthetic hub token: never a real one.
const testHubToken = "spool-hub-synthetic-token"

func newCloud(t *testing.T) (*Runtime, *cloudfake.Server) {
	t.Helper()
	fake := cloudfake.New(testAccount.Token)
	t.Cleanup(fake.Close)
	cloud := &Runtime{
		Client: &Client{
			BaseURL: fake.URL,
			Account: func(context.Context) (Account, error) {
				return testAccount, nil
			},
		},
		Dir:          t.TempDir(),
		AllowedHosts: func(context.Context) ([]string, error) { return []string{"api.anthropic.com", "hub.example.test"}, nil },
		Bounds:       Bounds{Poll: 5 * time.Millisecond, Status: 10 * time.Millisecond},
	}
	return cloud, fake
}

func testSpec() runtime.Spec {
	return runtime.Spec{
		LoopID:             "loop-1",
		LoopName:           "cirrus",
		Model:              "claude-haiku-5-5",
		AppendSystemPrompt: "You are cirrus.",
		MCPConfig:          claude.MCPConfigJSON("https://hub.example.test/mcp", testHubToken, nil),
		SessionID:          "hub-session-1",
	}
}

func start(t *testing.T, cloud *Runtime, spec runtime.Spec) runtime.Proc {
	t.Helper()
	if err := cloud.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	proc, err := cloud.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Kill(); proc.Wait() })
	return proc
}

// turn sends text and collects the events up to the turn's result.
func turn(t *testing.T, proc runtime.Proc, text string) []claude.Event {
	t.Helper()
	if err := proc.Send(text); err != nil {
		t.Fatal(err)
	}
	return collect(t, proc)
}

func types(events []claude.Event) string {
	var names []string
	for _, event := range events {
		names = append(names, event.Type)
	}
	return strings.Join(names, ",")
}

func TestEnsureKeepsTheLoopsEnvironment(t *testing.T) {
	cloud, fake := newCloud(t)
	spec := testSpec()
	ctx := context.Background()
	if err := cloud.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := cloud.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if len(fake.Environments) != 1 {
		t.Fatalf("two wakes made %d environments, want one", len(fake.Environments))
	}
	var environment *cloudfake.Environment
	for _, each := range fake.Environments {
		environment = each
	}
	config := environment.Body["config"].(map[string]any)
	script := config["init_script"].(string)
	if !strings.Contains(script, "'https://hub.example.test/mcp'") || !strings.Contains(script, "${"+HubTokenVar+"}") {
		t.Errorf("the setup script doesn't register the hub by its token's variable:\n%s", script)
	}
	if strings.Contains(script, testHubToken) {
		t.Error("the setup script carries the token itself")
	}
	network := config["network_config"].(map[string]any)
	if hosts := network["allowed_hosts"].([]any); len(hosts) != 2 || network["allow_default_hosts"] != false {
		t.Errorf("network config = %v, want the hub's two hosts and no defaults", network)
	}

	cloud.AllowedHosts = func(context.Context) ([]string, error) { return []string{"hub.example.test"}, nil }
	if err := cloud.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	network = environment.Body["config"].(map[string]any)["network_config"].(map[string]any)
	if hosts := network["allowed_hosts"].([]any); len(hosts) != 1 || len(fake.Environments) != 1 {
		t.Errorf("an edited allowlist left %v across %d environments", hosts, len(fake.Environments))
	}
}

func TestEnsureRefusesALoopWithNoHubAddress(t *testing.T) {
	cloud, fake := newCloud(t)
	spec := testSpec()
	spec.MCPConfig = ""
	if err := cloud.Ensure(context.Background(), spec); err == nil || len(fake.Environments) != 0 {
		t.Fatalf("Ensure with no hub address: %v, %d environments", err, len(fake.Environments))
	}
}

// A fresh session is created with its first message, the system prompt
// and the hub token as a session variable, and no MCP server of its own.
// The turn reads back as the runner reads a contained one: the echo, the
// environment's log and the control protocol are left out, and every event
// carries the hub's session id.
func TestAFreshSessionRunsItsFirstTurn(t *testing.T) {
	cloud, fake := newCloud(t)
	proc := start(t, cloud, testSpec())
	events := turn(t, proc, "hello")
	if got := types(events); got != "system,assistant,result" {
		t.Fatalf("the turn read back as %s", got)
	}
	for _, event := range events {
		if event.SessionID != "hub-session-1" {
			t.Errorf("a %s event carries session %q, want the hub's", event.Type, event.SessionID)
		}
	}
	if events[0].Init == nil || events[0].Init.SessionID != "hub-session-1" {
		t.Errorf("init = %+v", events[0].Init)
	}
	if result := events[2].Result; result == nil || result.IsError || result.CostUSD == 0 {
		t.Errorf("result = %+v, want a success with its cost", result)
	}

	session := fake.Newest()
	if session.Context["append_system_prompt"] != "You are cirrus." || session.Context["model"] != "claude-haiku-5-5" {
		t.Errorf("session context = %v", session.Context)
	}
	if env := session.Context["environment_variables"].(map[string]any); env[HubTokenVar] != testHubToken || len(env) != 1 {
		t.Errorf("session variables = %v, want the hub token alone", env)
	}
	if servers := session.Context["mcp_config"].(map[string]any)["mcpServers"].(map[string]any); len(servers) != 0 {
		t.Errorf("mcp_config names %v, want none", servers)
	}
}

// A resumed session carries on in the same cloud session, and reads only
// what came after the turns before.
func TestAResumedSessionReadsOnFromWhereItWasLeft(t *testing.T) {
	cloud, fake := newCloud(t)
	first := start(t, cloud, testSpec())
	turn(t, first, "one")
	_ = first.CloseStdin()
	if exit := first.Wait(); exit.Code != 0 {
		t.Fatalf("a closed session exits %d", exit.Code)
	}

	spec := testSpec()
	spec.SessionID, spec.ResumeID = "", "hub-session-1"
	second := start(t, cloud, spec)
	events := turn(t, second, "two")
	if len(fake.Sessions) != 1 {
		t.Fatalf("a resume made %d sessions, want one", len(fake.Sessions))
	}
	if text := events[1].Assistant.Text; text != "echo: two" {
		t.Errorf("the resumed turn read %q, want only its own", text)
	}
}

// A resume of a session the runtime holds no cloud session for ends as
// the CLI's does, so the runner starts a fresh one.
func TestAResumeOfAnUnknownSessionIsLost(t *testing.T) {
	cloud, _ := newCloud(t)
	spec := testSpec()
	spec.SessionID, spec.ResumeID = "", "hub-session-unknown"
	proc := start(t, cloud, spec)
	if exit := proc.Wait(); !claude.IsSessionNotFound(exit) {
		t.Fatalf("exit = %+v, want the CLI's session-not-found", exit)
	}
}

func TestAnArchivedSessionIsLost(t *testing.T) {
	cloud, fake := newCloud(t)
	first := start(t, cloud, testSpec())
	turn(t, first, "one")
	_ = first.CloseStdin()
	first.Wait()
	archived := fake.Newest()
	fake.Mu.Lock()
	archived.Archived = true
	fake.Mu.Unlock()

	spec := testSpec()
	spec.SessionID, spec.ResumeID = "", "hub-session-1"
	proc := start(t, cloud, spec)
	if err := proc.Send("two"); err != nil {
		t.Fatal(err)
	}
	if exit := proc.Wait(); !claude.IsSessionNotFound(exit) {
		t.Fatalf("exit = %+v, want the CLI's session-not-found", exit)
	}
}

// A fresh hub session, as a rotation starts, gets a cloud session of its
// own, and the one it replaces is archived.
func TestAFreshSessionArchivesTheOneItReplaces(t *testing.T) {
	cloud, fake := newCloud(t)
	first := start(t, cloud, testSpec())
	turn(t, first, "one")
	_ = first.CloseStdin()
	first.Wait()
	old := fake.Newest()

	spec := testSpec()
	spec.SessionID = "hub-session-2"
	turn(t, start(t, cloud, spec), "two")
	if len(fake.Sessions) != 2 || !old.Archived || fake.Newest().Archived {
		t.Fatalf("after a fresh session: %d sessions, old archived %v", len(fake.Sessions), old.Archived)
	}
}

func lastResult(t *testing.T, events []claude.Event) *claude.ResultInfo {
	t.Helper()
	result := events[len(events)-1].Result
	if result == nil {
		t.Fatal("no result")
	}
	return result
}

func TestASessionIdleWithNoResultEndsTheTurn(t *testing.T) {
	cloud, fake := newCloud(t)
	fake.Reply = func(session *cloudfake.Session, _ string) []map[string]any {
		return []map[string]any{cloudfake.Init(session), cloudfake.Text("thinking")}
	}
	result := lastResult(t, turn(t, start(t, cloud, testSpec()), "hello"))
	if !result.IsError || !strings.Contains(result.ResultText, "session_status idle") {
		t.Fatalf("result = %+v, want an error naming the idle status", result)
	}
}

func TestASessionThatParksEndsTheTurn(t *testing.T) {
	cloud, fake := newCloud(t)
	fake.After = StatusRequiresAction
	fake.Reply = func(session *cloudfake.Session, _ string) []map[string]any {
		return []map[string]any{cloudfake.Init(session)}
	}
	result := lastResult(t, turn(t, start(t, cloud, testSpec()), "hello"))
	if !result.IsError || !strings.Contains(result.ResultText, "requires_action") {
		t.Fatalf("result = %+v, want an error naming requires_action", result)
	}
}

func TestATurnPastItsBoundEnds(t *testing.T) {
	cloud, fake := newCloud(t)
	cloud.Bounds.Events = 3
	fake.Reply = func(session *cloudfake.Session, _ string) []map[string]any {
		events := []map[string]any{cloudfake.Init(session)}
		for range 5 {
			events = append(events, cloudfake.Text("more"))
		}
		return append(events, cloudfake.Result("done"))
	}
	events := turn(t, start(t, cloud, testSpec()), "hello")
	if result := lastResult(t, events); !result.IsError || !strings.Contains(result.ResultText, "bound") || len(events) != 4 {
		t.Fatalf("%d events, result = %+v; want three and an error naming the bound", len(events), result)
	}
}

func TestATurnWithNoEventEndsOnTheBackstop(t *testing.T) {
	cloud, fake := newCloud(t)
	cloud.Bounds.Quiet = 50 * time.Millisecond
	fake.Hold = true
	result := lastResult(t, turn(t, start(t, cloud, testSpec()), "hello"))
	if !result.IsError || !strings.Contains(result.ResultText, "no event") {
		t.Fatalf("result = %+v, want the quiet backstop", result)
	}
}

func TestAnUnreadableEventEndsTheTurn(t *testing.T) {
	cloud, fake := newCloud(t)
	fake.Reply = func(session *cloudfake.Session, _ string) []map[string]any {
		return []map[string]any{{"no_type": true}}
	}
	result := lastResult(t, turn(t, start(t, cloud, testSpec()), "hello"))
	if !result.IsError || !strings.Contains(result.ResultText, "can't read") {
		t.Fatalf("result = %+v, want an error naming the event", result)
	}
}

// One event over the response bound is skipped: the turn ends as an error,
// and the next turn reads on past it rather than failing the same way.
func TestAnEventOverTheBoundIsSkipped(t *testing.T) {
	cloud, fake := newCloud(t)
	fake.Reply = func(session *cloudfake.Session, _ string) []map[string]any {
		return []map[string]any{cloudfake.Init(session), cloudfake.Text(strings.Repeat("a", maxResponse))}
	}
	proc := start(t, cloud, testSpec())
	result := lastResult(t, turn(t, proc, "hello"))
	if !result.IsError || !strings.Contains(result.ResultText, "skipped") {
		t.Fatalf("result = %+v, want the event skipped", result)
	}
	fake.Reply = nil
	events := turn(t, proc, "again")
	if result := lastResult(t, events); result.IsError || result.ResultText != "echo: again" {
		t.Fatalf("the next turn: %+v", result)
	}
}

func TestKillInterruptsTheTurn(t *testing.T) {
	cloud, fake := newCloud(t)
	fake.Hold = true
	proc := start(t, cloud, testSpec())
	if err := proc.Send("hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fake.Newest() != nil })
	time.Sleep(20 * time.Millisecond)
	_ = proc.Kill()
	if exit := proc.Wait(); exit.Code == 0 {
		t.Fatalf("a killed session exits %+v", exit)
	}
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	for _, session := range fake.Sessions {
		if session.Interrupts != 1 {
			t.Fatalf("the kill sent %d interrupts", session.Interrupts)
		}
	}
}

// The turn after a kill reads its own events, not what the killed turn
// left behind.
func TestTheTurnAfterAKillSkipsWhatItLeft(t *testing.T) {
	cloud, fake := newCloud(t)
	fake.Hold = true
	first := start(t, cloud, testSpec())
	if err := first.Send("one"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fake.Newest() != nil })
	_ = first.Kill()
	first.Wait()
	fake.Release() // the killed turn's reply lands after the kill

	spec := testSpec()
	spec.SessionID, spec.ResumeID = "", "hub-session-1"
	events := turn(t, start(t, cloud, spec), "two")
	if text := events[1].Assistant.Text; text != "echo: two" {
		t.Fatalf("the turn after the kill read %q", text)
	}
}

func TestDestroyArchivesTheSessionAndTheEnvironment(t *testing.T) {
	cloud, fake := newCloud(t)
	proc := start(t, cloud, testSpec())
	turn(t, proc, "hello")
	_ = proc.CloseStdin()
	proc.Wait()
	if err := cloud.Destroy(context.Background(), "loop-1"); err != nil {
		t.Fatal(err)
	}
	for _, environment := range fake.Environments {
		if !environment.Archived {
			t.Error("the environment is not archived")
		}
	}
	if !fake.Newest().Archived {
		t.Error("the session is not archived")
	}
	if _, err := os.Stat(cloud.path("loop-1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the state file outlived the loop: %v", err)
	}
	if health, _ := cloud.Health(context.Background(), "loop-1"); !health.Missing {
		t.Errorf("health after destroy = %+v, want missing", health)
	}
}

func TestFilesAreUnsupported(t *testing.T) {
	cloud, _ := newCloud(t)
	if err := cloud.PutFile(context.Background(), "loop-1", "/etc/passwd", "x"); !errors.Is(err, runtime.ErrUnsupported) {
		t.Errorf("PutFile = %v", err)
	}
	if _, err := cloud.GetFile(context.Background(), "loop-1", "/", "/etc/passwd", 1<<20); !errors.Is(err, runtime.ErrUnsupported) {
		t.Errorf("GetFile = %v", err)
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

// A poll the API refuses for now, overloaded or rate-limited, is tried
// again with a backoff: the turn reads on rather than ending.
func TestATransientRefusalIsRetried(t *testing.T) {
	for _, status := range []int{529, 503, 429} {
		cloud, fake := newCloud(t)
		fake.Hold = true
		proc := start(t, cloud, testSpec())
		if err := proc.Send("hello"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return fake.Newest() != nil })
		fake.Mu.Lock()
		fake.Refusals, fake.RefuseStatus = 2, status
		fake.Mu.Unlock()
		fake.Release()
		events := collect(t, proc)
		if result := lastResult(t, events); result.IsError {
			t.Fatalf("%d twice mid-turn ended it: %+v", status, result)
		}
	}
}

// A refusal for good ends the turn at once, naming what came back.
func TestAPermanentRefusalEndsTheTurn(t *testing.T) {
	cloud, fake := newCloud(t)
	fake.Refusals, fake.RefuseStatus = 1, 400
	result := lastResult(t, turn(t, start(t, cloud, testSpec()), "hello"))
	if !result.IsError || !strings.Contains(result.ResultText, "answered 400") {
		t.Fatalf("result = %+v, want the 400 named", result)
	}
}

// Refusals for now that go on end the turn after a bounded run of them.
func TestTransientRefusalsInARowEndTheTurn(t *testing.T) {
	cloud, fake := newCloud(t)
	fake.Refusals, fake.RefuseStatus = 100, 529
	result := lastResult(t, turn(t, start(t, cloud, testSpec()), "hello"))
	if !result.IsError || !strings.Contains(result.ResultText, "polls in a row") {
		t.Fatalf("result = %+v, want the run of refusals named", result)
	}
}

func TestBackoffHonoursRetryAfter(t *testing.T) {
	if got := backoff(time.Second, 1, nil); got != time.Second {
		t.Errorf("first backoff = %s", got)
	}
	if got := backoff(time.Second, 3, nil); got != 4*time.Second {
		t.Errorf("third backoff = %s", got)
	}
	if got := backoff(time.Second, 1, &APIError{RetryAfter: 7 * time.Second}); got != 7*time.Second {
		t.Errorf("backoff with Retry-After 7 = %s", got)
	}
	if got := backoff(time.Second, 40, &APIError{RetryAfter: time.Hour}); got != maxBackoff {
		t.Errorf("backoff is not capped: %s", got)
	}
}

// A message the session never echoes, which it never took, ends the turn
// once the session reads idle twice, rather than on the two-hour backstop.
func TestAMessageNeverEchoedEndsTheTurn(t *testing.T) {
	cloud, fake := newCloud(t)
	proc := start(t, cloud, testSpec())
	turn(t, proc, "one")
	fake.Mu.Lock()
	fake.Drop = true
	fake.Mu.Unlock()
	result := lastResult(t, turn(t, proc, "two"))
	if !result.IsError || !strings.Contains(result.ResultText, "never reached the session") {
		t.Fatalf("result = %+v, want the lost message named", result)
	}
}

// collect reads a turn already sent, up to its result.
func collect(t *testing.T, proc runtime.Proc) []claude.Event {
	t.Helper()
	var events []claude.Event
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event, ok := <-proc.Events():
			if !ok {
				t.Fatalf("the events closed mid-turn after %d", len(events))
			}
			events = append(events, event)
			if event.Type == "result" {
				return events
			}
		case <-deadline:
			t.Fatalf("no result; events so far: %d", len(events))
		}
	}
}
