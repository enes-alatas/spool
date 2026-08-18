package docker

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
)

func TestExecArgvCarriesNoEnvValues(t *testing.T) {
	spec := runtime.Spec{
		LoopID:    "abc",
		WorkDir:   runtime.WorkstationHome,
		SessionID: "sess-1",
		Env: map[string]string{
			"CLAUDE_CODE_OAUTH_TOKEN": "sk-secret",
			"API_KEY":                 "hunter2",
		},
	}
	argv, err := execArgv(spec)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "sk-secret") || strings.Contains(joined, "hunter2") {
		t.Fatalf("secret value leaked into argv: %q", joined)
	}
	wantPrefix := []string{
		"exec", "--interactive", "--workdir", runtime.WorkstationHome,
		"--env", "API_KEY", "--env", "CLAUDE_CODE_OAUTH_TOKEN", // sorted, value-less
		"spool-ws-abc", "claude",
	}
	if !reflect.DeepEqual(argv[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("argv prefix = %q, want %q", argv[:len(wantPrefix)], wantPrefix)
	}
	if !strings.Contains(joined, "--session-id sess-1") {
		t.Fatalf("claude args missing from argv: %q", joined)
	}
	if strings.Contains(joined, "--tty") || contains(argv, "-t") {
		t.Fatalf("a TTY would break the stream protocol (ADR-0018): %q", joined)
	}
}

func TestExecArgvRequiresSession(t *testing.T) {
	if _, err := execArgv(runtime.Spec{LoopID: "abc"}); err == nil {
		t.Fatal("want the claude.Args session error to propagate")
	}
}

func TestRunArgv(t *testing.T) {
	spec := runtime.Spec{LoopID: "abc", LoopName: "greeter", MemMB: 4096, CPUs: 2}
	got := runArgv(spec, "spool-workstation")
	want := []string{
		"run", "--detach", "--init",
		"--name", "spool-ws-abc",
		"--restart", "unless-stopped",
		"--volume", "spool-ws-abc:/home/loop",
		"--label", "spool.loop.id=abc",
		"--label", "spool.loop.name=greeter",
		"--memory", "4096m",
		"--cpus", "2",
		"spool-workstation", "sleep", "infinity",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runArgv = %q, want %q", got, want)
	}
}

func TestRunArgvImageOverrideAndNoLimits(t *testing.T) {
	spec := runtime.Spec{LoopID: "abc", Image: "custom:tag"}
	got := strings.Join(runArgv(spec, "spool-workstation"), " ")
	if !strings.Contains(got, "custom:tag sleep infinity") {
		t.Fatalf("per-loop image not honoured: %q", got)
	}
	if strings.Contains(got, "--memory") || strings.Contains(got, "--cpus") {
		t.Fatalf("zero limits must produce no limit flags: %q", got)
	}
}

func TestParseFleet(t *testing.T) {
	out := []byte(`
{"Labels":"spool.loop.id=l1,spool.loop.name=greeter","State":"running","Status":"Up 3 hours"}
{"Labels":"spool.loop.name=dead,spool.loop.id=l2","State":"exited","Status":"Exited (137) 3 minutes ago"}
{"Labels":"other=x","State":"running","Status":"Up 1 hour"}
not json
`)
	fleet := parseFleet(out)
	if len(fleet) != 2 {
		t.Fatalf("fleet = %+v, want 2 workstations", fleet)
	}
	if up := fleet["l1"]; !up.Up || up.Detail != "" {
		t.Fatalf("l1 = %+v, want up with no detail", up)
	}
	if down := fleet["l2"]; down.Up || !strings.Contains(down.Detail, "Exited (137)") {
		t.Fatalf("l2 = %+v, want down with docker's status as detail", down)
	}
}

func TestNotFoundClassification(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"Error: No such object: spool-ws-abc", true},
		{"Error response from daemon: No such container: spool-ws-abc", true},
		{"Error response from daemon: get spool-ws-abc: no such volume", true},
		{"Cannot connect to the Docker daemon", false},
	}
	for _, testCase := range cases {
		err := &cliError{subcommand: "inspect", exitCode: 1, stderr: testCase.stderr, err: errors.New("exit status 1")}
		if notFound(err) != testCase.want {
			t.Errorf("notFound(%q) = %v, want %v", testCase.stderr, !testCase.want, testCase.want)
		}
	}
	if notFound(exec.ErrNotFound) {
		t.Error("a non-CLI error must not classify as not-found")
	}
}

func contains(list []string, item string) bool {
	for _, entry := range list {
		if entry == item {
			return true
		}
	}
	return false
}
