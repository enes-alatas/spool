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
	argv, err := execArgv(spec, nil)
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
	if _, err := execArgv(runtime.Spec{LoopID: "abc"}, nil); err == nil {
		t.Fatal("want the claude.Args session error to propagate")
	}
}

func TestRunArgv(t *testing.T) {
	spec := runtime.Spec{LoopID: "abc", LoopName: "greeter", MemMB: 4096, CPUs: 2}
	got := runArgv(spec, "spool-workstation", []string{"--network", "spool-egress"})
	want := []string{
		"run", "--detach", "--init",
		"--name", "spool-ws-abc",
		"--restart", "unless-stopped",
		"--volume", "spool-ws-abc:/home/loop",
		"--network", "spool-egress",
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
	got := strings.Join(runArgv(spec, "spool-workstation", nil), " ")
	if !strings.Contains(got, "custom:tag sleep infinity") {
		t.Fatalf("per-loop image not honoured: %q", got)
	}
	if strings.Contains(got, "--memory") || strings.Contains(got, "--cpus") {
		t.Fatalf("zero limits must produce no limit flags: %q", got)
	}
}

// A workstation behind the wall gets the proxy in its exec env and nothing
// else changes; one in front of it (no proxy image configured) keeps the
// host-gateway alias it used to reach the hub directly.
func TestEgressShapesTheWorkstation(t *testing.T) {
	walledRT := &Runtime{egressImage: "spool-egress", hubPort: "8080"}
	argv, err := execArgv(runtime.Spec{LoopID: "abc", SessionID: "sess-1"}, walledRT.egressEnv())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--env HTTPS_PROXY=http://spool-egress-proxy:3128",
		"--env http_proxy=http://spool-egress-proxy:3128",
		"--env NO_PROXY=localhost,127.0.0.1,::1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("exec env missing %q: %q", want, joined)
		}
	}

	open := (&Runtime{}).networkArgs()
	if !reflect.DeepEqual(open, []string{"--add-host", "host.docker.internal:host-gateway"}) {
		t.Errorf("without a proxy image the workstation must keep its own way to the hub, got %q", open)
	}
	walled := walledRT.networkArgs()
	if !reflect.DeepEqual(walled, []string{"--network", "spool-egress"}) {
		t.Errorf("walled workstation networkArgs = %q", walled)
	}
}

func TestEgressProxyRunArgv(t *testing.T) {
	rt := &Runtime{egressImage: "spool-egress", hubPort: "8080", egressAllow: []string{"internal.example"}}
	got := strings.Join(rt.egressRunArgv(), " ")
	if rt.egressSpecHash() == (&Runtime{egressImage: "spool-egress", hubPort: "9090"}).egressSpecHash() {
		// a proxy's run arguments are fixed at creation, so a moved hub has
		// to be visible as a different spec or the old wall would be kept
		t.Error("the spec hash must change when the hub moves")
	}
	if suite := (&Runtime{egressImage: "spool-egress-itest"}); suite.egressNetwork() != "spool-egress-itest" ||
		suite.egressContainer() != "spool-egress-itest-proxy" {
		// two fleets on one daemon must not share a wall
		t.Errorf("a second proxy image must get its own network and container, got %s/%s",
			suite.egressNetwork(), suite.egressContainer())
	}
	for _, want := range []string{
		"--network spool-egress",
		"--restart unless-stopped",
		"--add-host host.docker.internal:host-gateway",
		"spool-egress --listen :3128",
		// the hub is allowlisted on its own port and no other, so an
		// allowlisted gateway is not a tunnel to the operator's machine
		"--allow host.docker.internal:8080,internal.example",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("proxy run argv missing %q: %q", want, got)
		}
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
