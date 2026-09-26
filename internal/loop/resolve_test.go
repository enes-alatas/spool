package loop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// resolvingRuntime answers ResolveModel from a table; nothing else of the
// seam is used here.
type resolvingRuntime struct {
	runtime.Runtime
	ids map[string]string
}

func (rt resolvingRuntime) ResolveModel(_ context.Context, model string) (string, error) {
	if id, ok := rt.ids[model]; ok {
		return id, nil
	}
	return "", errors.New("no init")
}

func newTestModels(t *testing.T, ids map[string]string) (*Models, store.Store) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewModels(db, bus.New(), resolvingRuntime{ids: ids}, store.RuntimeDocker, "2.1.281", log), db
}

func resolutionFor(t *testing.T, models *Models, name string) store.ModelResolution {
	t.Helper()
	view, err := models.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range view.Aliases {
		if alias.Model == name {
			return alias
		}
	}
	for _, entry := range view.Custom {
		if entry.Model == name {
			return entry.ModelResolution
		}
	}
	t.Fatalf("%s is not on the list", name)
	return store.ModelResolution{}
}

func waitResolved(t *testing.T, models *Models, name string) store.ModelResolution {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if res := resolutionFor(t, models, name); res.Resolved != "" {
			return res
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never resolved", name)
	return store.ModelResolution{}
}

// Every family alias is listed from the start; one whose run fails shows
// bare rather than wrong.
func TestModelsResolveTheAliasesAtStart(t *testing.T) {
	models, _ := newTestModels(t, map[string]string{"opus": "claude-opus-5-5", "fable": "claude-fable-5-1", "haiku": "claude-haiku-4-5-20251001"})
	models.Start(context.Background())

	opus := waitResolved(t, models, "opus")
	if opus.Resolved != "claude-opus-5-5" || opus.Source != store.ResolutionProbe || opus.CLIVersion != "2.1.281" {
		t.Fatalf("opus = %+v, want the probe's id with the CLI version", opus)
	}
	waitResolved(t, models, "haiku")
	if sonnet := resolutionFor(t, models, "sonnet"); sonnet.Resolved != "" {
		t.Fatalf("sonnet's run failed but it resolved to %q", sonnet.Resolved)
	}
}

// Only a turn on the default runtime and image refreshes a resolution, and
// only for a name on the list (ADR-0033 item 4).
func TestModelsObserveOnlyTheDefaultRuntimesTurns(t *testing.T) {
	models, _ := newTestModels(t, nil)
	onDefault := &store.Loop{Runtime: store.RuntimeDocker}
	ownImage := &store.Loop{Runtime: store.RuntimeDocker, Image: "mine:latest"}
	bare := &store.Loop{Runtime: store.RuntimeBare}

	models.Observe(ownImage, "opus", "claude-opus-4-9")
	models.Observe(bare, "opus", "claude-opus-4-9")
	models.Observe(onDefault, "claude-typed-once", "claude-typed-once")
	if res := resolutionFor(t, models, "opus"); res.Resolved != "" {
		t.Fatalf("a turn off the default runtime or image resolved opus to %q", res.Resolved)
	}

	models.Observe(onDefault, "opus", "claude-opus-5-5")
	if res := resolutionFor(t, models, "opus"); res.Resolved != "claude-opus-5-5" || res.Source != store.ResolutionTurn {
		t.Fatalf("after a default-runtime turn: %+v, want its id as a turn's", res)
	}
}

// A probe finishing after a turn has reported leaves the turn's answer: it
// ran under the operator's login.
func TestModelsAProbeDoesNotOverwriteATurnSinceStart(t *testing.T) {
	models, db := newTestModels(t, map[string]string{"opus": "claude-opus-5-5"})
	models.Observe(&store.Loop{Runtime: store.RuntimeDocker}, "opus", "claude-opus-5-6")

	models.resolve(context.Background(), "opus")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		models.mu.Lock()
		running := models.inflight["opus"]
		models.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows, err := db.Models().Resolutions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Resolved != "claude-opus-5-6" || rows[0].Source != store.ResolutionTurn {
		t.Fatalf("resolutions = %+v, want the turn's kept", rows)
	}
}

// A loop's own model is handed to --model as the list's entries are, so it
// is held to the same rule; only empty differs, being the CLI's default.
func TestValidLoopModel(t *testing.T) {
	for _, good := range []string{"", "opus", "claude-opus-4-1"} {
		if err := ValidLoopModel(good); err != nil {
			t.Errorf("ValidLoopModel(%q) = %v, want nil", good, err)
		}
	}
	for _, bad := range []string{"-p", "--dangerously-skip-permissions", "claude opus", string(make([]byte, maxModelLen+1))} {
		if err := ValidLoopModel(bad); !errors.Is(err, ErrInvalidModel) {
			t.Errorf("ValidLoopModel(%q) = %v, want ErrInvalidModel", bad, err)
		}
	}
}

func TestModelsAddValidates(t *testing.T) {
	models, _ := newTestModels(t, map[string]string{"claude-opus-4-1": "claude-opus-4-1"})
	ctx := context.Background()
	for _, bad := range []string{"", "  ", "--dangerously-skip", "claude opus", string(make([]byte, maxModelLen+1))} {
		if _, err := models.Add(ctx, bad, ""); !errors.Is(err, ErrInvalidModel) {
			t.Errorf("Add(%q): err = %v, want ErrInvalidModel", bad, err)
		}
	}
	if _, err := models.Add(ctx, "opus", ""); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("Add(opus): err = %v, want ErrDuplicate, since aliases are listed", err)
	}
	entry, err := models.Add(ctx, " claude-opus-4-1 ", " pinned ")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Model != "claude-opus-4-1" || entry.Label != "pinned" || entry.Resolved != "" {
		t.Fatalf("entry = %+v, want it trimmed and not yet resolved", entry)
	}
	if res := waitResolved(t, models, "claude-opus-4-1"); res.Source != store.ResolutionProbe {
		t.Fatalf("custom entry resolution = %+v", res)
	}
	if _, err := models.Add(ctx, "claude-opus-4-1", ""); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("second Add: err = %v, want ErrDuplicate", err)
	}
}
