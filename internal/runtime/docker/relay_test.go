package docker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/egress"
)

// relayDocker is a docker CLI whose proxy is on the wall's network and two
// loop networks. It logs each call's arguments, one line a call, and keeps
// the stdin of the last exec. While its failing file exists, an exec fails.
type relayDocker struct {
	bin, argv, handed, failing string
}

const syntheticRelayAuthorization = "Basic c3ludGhldGljLXB1YmxpYzpzeW50aGV0aWMtc2VjcmV0"

func newRelayDocker(t *testing.T) relayDocker {
	t.Helper()
	dir := t.TempDir()
	fake := relayDocker{bin: filepath.Join(dir, "docker"), argv: filepath.Join(dir, "argv"), handed: filepath.Join(dir, "handed"), failing: filepath.Join(dir, "failing")}
	networks := `{"spool-egress":{"IPAddress":"172.30.0.2"},"spool-egress-loop-loop-a":{"IPAddress":"10.1.0.2"},"spool-egress-loop-loop-b":{"IPAddress":"10.2.0.2"},"bridge":{"IPAddress":"172.17.0.5"}}`
	script := "#!/bin/sh\necho \"$*\" >>" + fake.argv + "\n" +
		"case \"$1\" in\n" +
		"inspect) echo '" + networks + "' ;;\n" +
		"exec) [ -e " + fake.failing + " ] && exit 1; cat >" + fake.handed + " ;;\n" +
		"*) cat >/dev/null ;;\n" +
		"esac\n"
	if err := os.WriteFile(fake.bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return fake
}

func (fake relayDocker) execs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(fake.argv)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var execs []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "exec ") {
			execs = append(execs, line)
		}
	}
	return execs
}

func (fake relayDocker) file(t *testing.T) egress.RelayFile {
	t.Helper()
	data, err := os.ReadFile(fake.handed)
	if err != nil {
		t.Fatal(err)
	}
	var file egress.RelayFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestSetRelayHandsTheProxyItsFileOnStdin(t *testing.T) {
	fake := newRelayDocker(t)
	rt := New(Options{Bin: fake.bin, EgressImage: "spool-egress"})
	ctx := context.Background()
	rt.relayLoopSeen("loop-a", "alpha")
	rt.relayLoopSeen("loop-b", "beta")
	if err := rt.SetRelay(ctx, RelaySettings{
		Upstream:      "http://langfuse-web",
		ReadAddress:   "http://127.0.0.1:3000",
		Authorization: syntheticRelayAuthorization,
	}); err != nil {
		t.Fatal(err)
	}

	execs := fake.execs(t)
	if len(execs) != 1 || execs[0] != "exec --interactive spool-egress-proxy /spool-egress relay-config" {
		t.Fatalf("execs = %q, want one relay-config exec into the proxy", execs)
	}
	argv, _ := os.ReadFile(fake.argv)
	if strings.Contains(string(argv), "c3ludGhldGlj") {
		t.Error("the ingest key crossed docker's argv")
	}
	file := fake.file(t)
	if file.Upstream != "http://langfuse-web" || file.Authorization != syntheticRelayAuthorization {
		t.Errorf("handed upstream %q, authorization set %v", file.Upstream, file.Authorization != "")
	}
	wantLoops := []egress.RelayLoop{
		{Address: "10.1.0.2", ID: "loop-a", Name: "alpha"},
		{Address: "10.2.0.2", ID: "loop-b", Name: "beta"},
	}
	if !slices.Equal(file.Loops, wantLoops) {
		t.Errorf("loops = %+v, want %+v (the wall's own network and the bridge are no loop's)", file.Loops, wantLoops)
	}
	if !slices.Equal(file.Langfuse, []string{"langfuse-web", "127.0.0.1"}) {
		t.Errorf("Langfuse hosts = %q, want both addresses' hosts", file.Langfuse)
	}
}

func TestTheRelayIsHandedAgainOnlyWhenSomethingMoved(t *testing.T) {
	fake := newRelayDocker(t)
	rt := New(Options{Bin: fake.bin, EgressImage: "spool-egress"})
	ctx := context.Background()
	if err := rt.catchUpRelay(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.execs(t)); got != 0 {
		t.Fatalf("a run never given settings handed %d files, want none", got)
	}
	if err := rt.SetRelay(ctx, RelaySettings{Upstream: "https://langfuse.example", Authorization: syntheticRelayAuthorization}); err != nil {
		t.Fatal(err)
	}
	rt.relayProxyStarted("2026-11-05T10:00:00Z")
	if err := rt.catchUpRelay(ctx); err != nil {
		t.Fatal(err)
	}
	handed := len(fake.execs(t))

	if err := rt.catchUpRelay(ctx); err != nil {
		t.Fatal(err)
	}
	rt.relayLoopSeen("loop-a", "")
	rt.relayProxyStarted("2026-11-05T10:00:00Z")
	if err := rt.catchUpRelay(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.execs(t)); got != handed {
		t.Fatalf("nothing moved, yet %d more files were handed", got-handed)
	}

	for name, move := range map[string]func(){
		"a renamed loop":    func() { rt.relayLoopSeen("loop-a", "alpha-2") },
		"a restarted proxy": func() { rt.relayProxyStarted("2026-11-05T11:00:00Z") },
		"a joined network":  rt.relayMoved,
	} {
		before := len(fake.execs(t))
		move()
		if err := rt.catchUpRelay(ctx); err != nil {
			t.Fatal(err)
		}
		if got := len(fake.execs(t)); got != before+1 {
			t.Errorf("after %s, %d files handed, want 1", name, got-before)
		}
	}
}

func TestClearingTheUpstreamHandsARelayTurnedOff(t *testing.T) {
	fake := newRelayDocker(t)
	rt := New(Options{Bin: fake.bin, EgressImage: "spool-egress"})
	ctx := context.Background()
	if err := rt.SetRelay(ctx, RelaySettings{Upstream: "https://langfuse.example", Authorization: syntheticRelayAuthorization}); err != nil {
		t.Fatal(err)
	}
	if err := rt.SetRelay(ctx, RelaySettings{ReadAddress: "https://langfuse.example", Authorization: syntheticRelayAuthorization}); err != nil {
		t.Fatal(err)
	}
	file := fake.file(t)
	if file.Upstream != "" || file.Authorization != "" || len(file.Loops) != 0 {
		t.Errorf("handed %+v, want no upstream, no key and no loops", file)
	}
	if !slices.Equal(file.Langfuse, []string{"langfuse.example"}) {
		t.Errorf("Langfuse hosts = %q, want them still refused", file.Langfuse)
	}
}

func TestTheProxyRunsTheRelayOnATmpfs(t *testing.T) {
	rt := New(Options{Bin: "docker", EgressImage: "spool-egress", MCPPort: "8081"})
	argv := strings.Join(rt.egressRunArgv("sha256:abc"), " ")
	for _, want := range []string{
		"--tmpfs " + relayDir + ":",
		"--relay-listen :" + egress.RelayPort,
		"--relay-file " + egress.RelayFilePath,
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("run argv lacks %q: %s", want, argv)
		}
	}
	if filepath.Dir(egress.RelayFilePath) != relayDir {
		t.Errorf("the relay file %s is outside the tmpfs %s", egress.RelayFilePath, relayDir)
	}
}

// A removed loop's network outlives no entry in the relay file: the proxy
// is handed a file without it before the network goes, and a failed
// hand-over keeps the network, so docker can't hand its subnet to the
// next loop while the proxy still names the removed one.
func TestARemovedLoopNetworkWaitsForTheRelayToForgetIt(t *testing.T) {
	fake := newRelayDocker(t)
	rt := New(Options{Bin: fake.bin, EgressImage: "spool-egress"})
	ctx := context.Background()
	if err := rt.SetRelay(ctx, RelaySettings{Upstream: "https://langfuse.example", Authorization: syntheticRelayAuthorization}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fake.failing, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rt.removeLoopNetwork(ctx, "loop-a"); err == nil {
		t.Fatal("removed with the hand-over failing, want its error")
	}
	argv, _ := os.ReadFile(fake.argv)
	if strings.Contains(string(argv), "network rm") {
		t.Fatal("the network was removed while the proxy still named its loop")
	}

	if err := os.Remove(fake.failing); err != nil {
		t.Fatal(err)
	}
	before := len(fake.execs(t))
	if err := rt.removeLoopNetwork(ctx, "loop-a"); err != nil {
		t.Fatal(err)
	}
	argv, _ = os.ReadFile(fake.argv)
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	removed := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "network rm ") })
	lastHanded := -1
	for index, line := range lines {
		if strings.HasPrefix(line, "exec ") {
			lastHanded = index
		}
	}
	if got := len(fake.execs(t)); got != before+1 || removed < 0 {
		t.Fatalf("after the retry: %d hand-overs, network removed at line %d; want one, then the removal", got-before, removed)
	}
	if lastHanded > removed {
		t.Errorf("the hand-over came after the network was removed: %q", lines)
	}
}

// An upstream with no key to send it can't let a trace in, so the relay
// is handed off, with the upstream's host still refused to the proxy.
func TestAnUpstreamWithNoKeyHandsARelayTurnedOff(t *testing.T) {
	fake := newRelayDocker(t)
	rt := New(Options{Bin: fake.bin, EgressImage: "spool-egress"})
	if err := rt.SetRelay(context.Background(), RelaySettings{Upstream: "https://langfuse.example"}); err != nil {
		t.Fatal(err)
	}
	file := fake.file(t)
	if file.Upstream != "" || file.Authorization != "" || len(file.Loops) != 0 {
		t.Errorf("handed %+v, want the relay off", file)
	}
	if !slices.Equal(file.Langfuse, []string{"langfuse.example"}) {
		t.Errorf("Langfuse hosts = %q, want the upstream's still refused", file.Langfuse)
	}
}
