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

// A write that fails leaves the map as the proxy last read it, so the next
// wake retries the change instead of finding it already made.
func TestLoopEgressRetriesAFailedWrite(t *testing.T) {
	bin, calls, failing := fakeDocker(t)
	rt := New(Options{Bin: bin, EgressImage: "spool-egress"})
	ctx := context.Background()
	attached := runtime.Spec{LoopID: "abc", EgressToken: "synthetic-token", EgressAllow: []string{"mcp.example.com"}}
	detached := runtime.Spec{LoopID: "abc", EgressToken: "synthetic-token"}

	setFailing(t, failing, true)
	if _, err := rt.openLoopEgress(ctx, attached); err == nil {
		t.Fatal("attach: want the failed write's error")
	}
	setFailing(t, failing, false)
	if tokened, err := rt.openLoopEgress(ctx, attached); err != nil || !tokened {
		t.Fatalf("attach retry = %v, %v; want true, nil", tokened, err)
	}
	if got := countCalls(t, calls); got != 2 {
		t.Fatalf("attach: %d writes, want the retry to write again (2)", got)
	}

	setFailing(t, failing, true)
	if _, err := rt.openLoopEgress(ctx, detached); err == nil {
		t.Fatal("detach: want the failed write's error")
	}
	setFailing(t, failing, false)
	if tokened, err := rt.openLoopEgress(ctx, detached); err != nil || tokened {
		t.Fatalf("detach retry = %v, %v; want false, nil", tokened, err)
	}
	if got := countCalls(t, calls); got != 4 {
		t.Fatalf("detach: %d writes, want the retry to write again (4)", got)
	}

	if _, err := rt.openLoopEgress(ctx, attached); err != nil {
		t.Fatal(err)
	}
	setFailing(t, failing, true)
	if err := rt.closeLoopEgress(ctx, "abc"); err == nil {
		t.Fatal("close: want the failed write's error")
	}
	setFailing(t, failing, false)
	if err := rt.closeLoopEgress(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if got := countCalls(t, calls); got != 7 {
		t.Fatalf("close: %d writes, want the retry to write again (7)", got)
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
