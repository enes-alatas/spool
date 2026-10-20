package cloud

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// A host stays as it is, a domain rule is its domain and everything under
// it, and an entry with a port of its own, a stored * or a stored domain of
// one label has no form in a cloud environment, so it is left off and
// named: the list is never wider than the hub's.
func TestAllowlistTranslatesTheHubsEntries(t *testing.T) {
	hosts, notApplied := Allowlist([]string{
		"pypi.org", ".example.test", "registry.example.test:8443", "example.test", "pypi.org", "registry.example.test:8443", "*", "*.stored.test", ".stored-tld",
	})
	if want := []string{"pypi.org", "*.example.test", "example.test"}; !slices.Equal(hosts, want) {
		t.Errorf("hosts = %q, want %q", hosts, want)
	}
	if want := []string{"registry.example.test:8443", "*", "*.stored.test", ".stored-tld"}; !slices.Equal(notApplied, want) {
		t.Errorf("not applied = %q, want %q", notApplied, want)
	}
}

// allowedHostsOf reads the allowed hosts an environment was last written
// with.
func allowedHostsOf(t *testing.T, body map[string]any) []string {
	t.Helper()
	var hosts []string
	for _, host := range body["config"].(map[string]any)["network_config"].(map[string]any)["allowed_hosts"].([]any) {
		hosts = append(hosts, host.(string))
	}
	return hosts
}

func secondSpec() runtime.Spec {
	spec := testSpec()
	spec.LoopID, spec.LoopName = "loop-2", "stratus"
	return spec
}

// An edit to the allowlist reaches every environment at Sync, with no wake
// of its loop, and Syncs reads each as in sync with what it left off.
func TestSyncWritesEveryEnvironmentWithNoWake(t *testing.T) {
	cloud, fake := newCloud(t)
	ctx := context.Background()
	for _, spec := range []runtime.Spec{testSpec(), secondSpec()} {
		if err := cloud.Ensure(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	cloud.EgressEntries = func(context.Context) ([]string, error) {
		return []string{"hub.example.test", ".example.test", "registry.example.test:8443"}, nil
	}
	syncs, _, err := cloud.Syncs(ctx)
	if err != nil || len(syncs) != 2 || syncs[0].InSync || syncs[1].InSync {
		t.Fatalf("before Sync: %+v, %v; want both environments out of sync", syncs, err)
	}

	if err := cloud.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"hub.example.test", "*.example.test", "example.test"}
	for _, environment := range fake.Environments {
		if hosts := allowedHostsOf(t, environment.Body); !slices.Equal(hosts, want) {
			t.Errorf("environment %s allows %q, want %q", environment.ID, hosts, want)
		}
	}
	syncs, notApplied, err := cloud.Syncs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(syncs) != 2 || syncs[0].LoopID != "loop-1" || syncs[1].LoopID != "loop-2" {
		t.Fatalf("syncs = %+v, want loop-1 and loop-2", syncs)
	}
	for _, sync := range syncs {
		if !sync.InSync || sync.Error != "" || sync.SyncedAt == 0 {
			t.Errorf("after Sync: %+v, want in sync", sync)
		}
	}
	if !slices.Equal(notApplied, []string{"registry.example.test:8443"}) {
		t.Errorf("not applied = %q", notApplied)
	}
}

// A write that fails is named by its loop, read as out of sync with its
// reason, and made by the next Sync.
func TestAFailedSyncIsRecordedAndRetried(t *testing.T) {
	cloud, fake := newCloud(t)
	ctx := context.Background()
	if err := cloud.Ensure(ctx, testSpec()); err != nil {
		t.Fatal(err)
	}
	cloud.EgressEntries = func(context.Context) ([]string, error) { return []string{"hub.example.test", "pypi.org"}, nil }
	fake.Mu.Lock()
	fake.RefuseUpdates = true
	fake.Mu.Unlock()
	if err := cloud.Sync(ctx); err == nil || !strings.Contains(err.Error(), "loop-1") {
		t.Fatalf("Sync = %v, want the failure named by its loop", err)
	}
	syncs, _, err := cloud.Syncs(ctx)
	if err != nil || len(syncs) != 1 || syncs[0].InSync || !strings.Contains(syncs[0].Error, "could not be updated") {
		t.Fatalf("after a failed write: %+v, %v; want out of sync with the reason", syncs, err)
	}

	fake.Mu.Lock()
	fake.RefuseUpdates = false
	fake.Mu.Unlock()
	if err := cloud.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if syncs, _, _ := cloud.Syncs(ctx); len(syncs) != 1 || !syncs[0].InSync || syncs[0].Error != "" {
		t.Fatalf("after the retry: %+v, want in sync", syncs)
	}
}

// A wake that read the allowlist before an edit narrowed it can't write
// the wider list back over the edit's Sync: the narrowed list is what the
// environment ends on, and what Syncs reads it as in sync with.
func TestAWakeCantUndoANarrowingSync(t *testing.T) {
	cloud, fake := newCloud(t)
	ctx := context.Background()
	wide := []string{"hub.example.test", "pypi.org"}
	narrowed := []string{"hub.example.test"}
	var (
		mu      sync.Mutex
		entries = wide
		block   bool
	)
	reading, release := make(chan struct{}), make(chan struct{})
	cloud.EgressEntries = func(context.Context) ([]string, error) {
		mu.Lock()
		read, blocks := entries, block
		block = false
		mu.Unlock()
		if blocks {
			close(reading)
			<-release
		}
		return read, nil
	}
	if err := cloud.Ensure(ctx, testSpec()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	block = true
	mu.Unlock()
	woke := make(chan error, 1)
	go func() { woke <- cloud.Ensure(ctx, testSpec()) }()
	<-reading
	mu.Lock()
	entries = narrowed
	mu.Unlock()
	synced := make(chan error, 1)
	go func() { synced <- cloud.Sync(ctx) }()
	// Sync waits for the wake; without the lock it lands here first.
	select {
	case err := <-synced:
		synced <- err
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-woke; err != nil {
		t.Fatal(err)
	}
	if err := <-synced; err != nil {
		t.Fatal(err)
	}

	for _, environment := range fake.Environments {
		if hosts := allowedHostsOf(t, environment.Body); !slices.Equal(hosts, narrowed) {
			t.Errorf("the environment allows %q, want the narrowed %q", hosts, narrowed)
		}
	}
	if syncs, _, err := cloud.Syncs(ctx); err != nil || len(syncs) != 1 || !syncs[0].InSync {
		t.Errorf("syncs = %+v, %v; want in sync with the narrowed list", syncs, err)
	}
}

// A resume under a hub token other than the one its cloud session was
// created with is lost, as an unknown one is, so the runner starts a fresh
// session that carries the new token. The session holds its token for its
// life, and the hub would refuse the old one.
func TestAResumeUnderAReplacedTokenIsLost(t *testing.T) {
	cloud, _ := newCloud(t)
	first := start(t, cloud, testSpec())
	turn(t, first, "one")
	_ = first.CloseStdin()
	first.Wait()

	spec := testSpec()
	spec.SessionID, spec.ResumeID = "", "hub-session-1"
	spec.MCPConfig = claude.MCPConfigJSON("https://hub.example.test/mcp", "spool-hub-synthetic-replaced", nil)
	proc := start(t, cloud, spec)
	lost := make(chan bool, 1)
	go func() { lost <- claude.IsSessionNotFound(proc.Wait()) }()
	select {
	case ok := <-lost:
		if !ok {
			t.Fatal("the resume ended, but not as the CLI's session-not-found")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the resume carried on in the session created with the old token")
	}
}
