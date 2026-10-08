package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
)

// fakeDocker is a docker CLI that counts its calls in a file and fails while
// a second file exists.
func fakeDocker(t *testing.T) (bin, calls, failing string) {
	t.Helper()
	dir := t.TempDir()
	bin, calls, failing = filepath.Join(dir, "docker"), filepath.Join(dir, "calls"), filepath.Join(dir, "failing")
	script := "#!/bin/sh\ncat >/dev/null\necho \"$1\" >>" + calls + "\n[ ! -e " + failing + " ]\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls, failing
}

func countCalls(t *testing.T, calls string) int {
	t.Helper()
	data, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func setFailing(t *testing.T, failing string, fail bool) {
	t.Helper()
	var err error
	if fail {
		err = os.WriteFile(failing, nil, 0o644)
	} else {
		err = os.Remove(failing)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// The operator's extra hosts reach the proxy through its file, without
// recreating it: applied once a copy lands, not before. A hub copies
// nothing before its own first wake has written the file, since the proxy
// may be another hub's (#542).
func TestFleetEgressAppliesThroughTheFile(t *testing.T) {
	bin, calls, failing := fakeDocker(t)
	rt := New(Options{Bin: bin, EgressImage: "spool-egress"})
	ctx := context.Background()
	if err := rt.SetFleetEgress(ctx, []string{"pkg.example"}); err != nil {
		t.Fatal(err)
	}
	if got := countCalls(t, calls); got != 0 || !rt.FleetEgressApplied().IsZero() {
		t.Fatalf("before the first wake: %d copies, applied %v; want none", got, rt.FleetEgressApplied())
	}
	if err := rt.catchUpEgressFile(ctx); err != nil {
		t.Fatal(err)
	}
	if rt.FleetEgressApplied().IsZero() {
		t.Fatal("not applied after the first wake's copy")
	}

	setFailing(t, failing, true)
	if err := rt.SetFleetEgress(ctx, []string{"pkg.example", "tracker.example"}); err == nil {
		t.Fatal("want the failed copy's error")
	}
	if !rt.FleetEgressApplied().IsZero() {
		t.Error("applied after a failed copy")
	}
	setFailing(t, failing, false)
	if err := rt.catchUpEgressFile(ctx); err != nil {
		t.Fatal(err)
	}
	if rt.FleetEgressApplied().IsZero() {
		t.Fatal("not applied after the next wake's copy")
	}

	before := countCalls(t, calls)
	if err := rt.SetFleetEgress(ctx, []string{"pkg.example", "tracker.example"}); err != nil {
		t.Fatal(err)
	}
	if got := countCalls(t, calls); got != before {
		t.Errorf("setting the list it holds copied %d more times, want none", got-before)
	}
	if err := rt.catchUpEgressFile(ctx); err != nil || countCalls(t, calls) != before {
		t.Errorf("catching up a proxy that holds the list copied again (%v)", err)
	}
	if err := rt.SetFleetEgress(ctx, []string{"pkg.example"}); err != nil {
		t.Fatal(err)
	}
	if rt.FleetEgressApplied().IsZero() {
		t.Error("not applied after a change that copied")
	}
	if got := countCalls(t, calls); got != before+1 {
		t.Errorf("a change copied %d times, want 1", got-before)
	}
}

// A wake of a workstation that already runs copies the fleet's hosts into
// a proxy that already runs, as the first docker wake of a hub run whose
// workstations all survived from the last one is (#657). Before, only a
// provision ensured the wall, so such a hub never wrote the file, and a
// host added on Settings never reached the proxy.
func TestAWakeOfARunningWorkstationCatchesUpTheProxy(t *testing.T) {
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
	rt := New(Options{Bin: bin, EgressImage: "spool-egress"})
	// a docker whose every container runs, the proxy on this hub's spec
	script := "#!/bin/sh\ncat >/dev/null\necho \"$*\" >>" + calls + "\n" +
		"case \"$*\" in\n" +
		"*'.Config.Labels'*) echo '{\"" + egressSpecLabel + "\":\"" + rt.egressSpecHash() + "\"}' ;;\n" +
		"*'.State'*) echo '{\"Running\":true}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := rt.SetFleetEgress(ctx, []string{"uploads.example"}); err != nil {
		t.Fatal(err)
	}
	if !rt.FleetEgressApplied().IsZero() {
		t.Fatal("applied before any wake of this hub run")
	}
	if err := rt.Ensure(ctx, runtime.Spec{LoopID: "survivor"}); err != nil {
		t.Fatal(err)
	}
	if rt.FleetEgressApplied().IsZero() {
		data, _ := os.ReadFile(calls)
		t.Fatalf("a wake of a running workstation left the fleet's hosts unapplied; docker was called with:\n%s", data)
	}
}
