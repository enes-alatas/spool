package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// loopsReadThen is a loop store that runs then once, right after its
// first Get has read the row: what another goroutine does between the
// read and what the reader does next.
type loopsReadThen struct {
	store.LoopStore
	then func()
}

func (loops *loopsReadThen) Get(ctx context.Context, id string) (*store.Loop, error) {
	loopRecord, err := loops.LoopStore.Get(ctx, id)
	if then := loops.then; then != nil {
		loops.then = nil
		then()
	}
	return loopRecord, err
}

// storeWithLoops is a store whose Loops is loops.
type storeWithLoops struct {
	store.Store
	loops store.LoopStore
}

func (db storeWithLoops) Loops() store.LoopStore { return db.loops }

// A check that stores its result and ends just after the setup view read
// the loop's row shows as under way, not as the check before it: the
// view asks whether one is under way before it reads the row, so a poll
// waiting for the check to end reads its result next (#1243).
func TestASetupViewNeverShowsTheCheckBeforeTheOneThatEnded(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	loopRecord := store.Loop{ID: "l-cirrus", Name: "cirrus", Runtime: store.RuntimeCloud,
		Status: store.StatusActive, WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, CreatedAt: 1, UpdatedAt: 1}
	if err := db.Loops().Create(ctx, &loopRecord); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().SetCloudCheck(ctx, loopRecord.ID, &store.CloudCheck{State: store.CloudCheckOK, CheckedAt: 1}); err != nil {
		t.Fatal(err)
	}
	loops := &loopsReadThen{LoopStore: db.Loops()}
	server := &Server{Store: storeWithLoops{Store: db, loops: loops}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	view := func() *cloudLoopCheckView {
		t.Helper()
		recorder := httptest.NewRecorder()
		server.answerCloudLoopSetup(recorder, httptest.NewRequest("GET", "/api/loops/cirrus/cloud/setup", nil), http.StatusOK, loopRecord.ID)
		var setup cloudLoopSetupView
		if err := json.NewDecoder(recorder.Body).Decode(&setup); err != nil {
			t.Fatalf("the setup view (%d): %v", recorder.Code, err)
		}
		return setup.Check
	}

	if !server.cloudLoopChecks.begin(loopRecord.ID) {
		t.Fatal("a check is already under way")
	}
	loops.then = func() {
		failed := &store.CloudCheck{State: store.CloudCheckFailed, Stage: cloudStepEnvironment, CheckedAt: 2}
		if err := db.Loops().SetCloudCheck(ctx, loopRecord.ID, failed); err != nil {
			t.Error(err)
		}
		server.cloudLoopChecks.end(loopRecord.ID)
	}
	if check := view(); check == nil || check.State != cloudCheckRunning {
		t.Fatalf("the view read as the check ended = %+v, want it under way", check)
	}
	if check := view(); check == nil || check.State != store.CloudCheckFailed || check.Stage != cloudStepEnvironment {
		t.Fatalf("the view after the check ended = %+v, want its failure", check)
	}
}
