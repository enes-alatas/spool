package loop

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// checkingRuntime is a bare runtime whose login checks answer, in turn,
// what the test sends them; nothing else of the seam is used here.
type checkingRuntime struct {
	runtime.Runtime
	answers chan claude.LoginCheck
}

func (checkingRuntime) Kind() string { return store.RuntimeBare }

func (rt checkingRuntime) CheckLogin(ctx context.Context, _ string) (claude.LoginCheck, error) {
	select {
	case answer := <-rt.answers:
		return answer, nil
	case <-ctx.Done():
		return claude.LoginCheck{}, ctx.Err()
	}
}

// TestALoginCheckKeepsOnlyTheNewestOutcome: Start stores a pending record
// at once, and a check started while another runs supersedes it, so the
// older one's late answer is dropped whichever way it went (ADR-0044).
func TestALoginCheckKeepsOnlyTheNewestOutcome(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	rt := checkingRuntime{answers: make(chan claude.LoginCheck)}
	checker := NewLoginChecker(db, rt, slog.New(slog.NewTextHandler(io.Discard, nil)))
	last := func() store.LoginCheckRecord {
		t.Helper()
		record, err := checker.Last(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}

	if got := last(); got.Status != "" {
		t.Fatalf("before any check: %+v, want the zero record", got)
	}
	if err := checker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if got := last(); got.Status != store.LoginCheckPending {
		t.Fatalf("right after Start: %+v, want pending", got)
	}
	first := last().At
	if err := checker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	second := last().At
	if second <= first {
		t.Fatalf("the second check began at %d, not after the first's %d", second, first)
	}

	// Two checks are running; whichever takes an answer first, the
	// refusal is sent before the acceptance.
	rt.answers <- claude.LoginCheck{Refusal: "Failed to authenticate"}
	rt.answers <- claude.LoginCheck{OK: true}
	for deadline := time.Now().Add(5 * time.Second); last().Status == store.LoginCheckPending; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the newest check never finished")
		}
	}
	time.Sleep(50 * time.Millisecond) // let the other answer land, if it would
	if got := last(); got.At != second {
		t.Fatalf("kept %+v, want the second check's outcome (began %d)", got, second)
	}

	if err := checker.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if got := last(); got.Status != "" {
		t.Fatalf("after Clear: %+v, want the zero record", got)
	}
}

// TestALoginCheckLeftPendingReadsInconclusive: a hub stopped mid-check
// leaves a pending record no run will finish, so once the check's bound has
// passed it reads as one that did not finish, not as one still running.
func TestALoginCheckLeftPendingReadsInconclusive(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{time.Second, store.LoginCheckPending},
		{loginCheckTimeout + time.Second, store.LoginCheckInconclusive},
	} {
		at := time.Now().Add(-tc.age).UnixMilli()
		raw, _ := json.Marshal(store.LoginCheckRecord{Status: store.LoginCheckPending, At: at})
		if err := db.Settings().Set(ctx, store.SettingLoginCheck, string(raw)); err != nil {
			t.Fatal(err)
		}
		got, err := LastLoginCheck(ctx, db.Settings())
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tc.want || got.At != at {
			t.Errorf("a check pending for %v reads %+v, want %s at %d", tc.age, got, tc.want, at)
		}
	}
}
