package loop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// A workstation down for a calm reason is not a failed wake (#1198): one
// switched off reads no failure on the first-run page, and one not yet
// built is not down at all, while every other reason reads workstation,
// over whatever the loop's last try recorded. Up again, the record shows
// through.
func TestOnlyAnAlarmingDownIsAFailedWake(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	loopRecord := store.Loop{
		ID: "l1", Name: "faltering", Mission: "m", Model: "haiku", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := db.Loops().Create(context.Background(), &loopRecord); err != nil {
		t.Fatal(err)
	}
	actor := &Actor{deps: Deps{Store: db, Bus: bus.New()}, loop: loopRecord, downTimer: time.NewTimer(time.Hour)}
	actor.downTimer.Stop()
	actor.noteWakeFailure(WakeFailureCrashed)

	for _, row := range []struct {
		reason string
		off    bool
		want   string
	}{
		// not built yet never raises workstation_down, so the record shows
		{reason: DownReasonNotProvisioned, want: WakeFailureCrashed},
		{reason: DownReasonUnreachable, off: true, want: ""},
		{reason: DownReasonUnauthenticated, want: WakeFailureWorkstation},
		{reason: DownReasonUnreachable, want: WakeFailureWorkstation},
		{reason: DownReasonHubUnreachable, want: WakeFailureWorkstation},
	} {
		actor.offSnap.Store(row.off)
		actor.setWorkstationDown(row.reason, "")
		if got := actor.WakeFailure(); got != row.want {
			t.Errorf("down for %s (switched off: %v) reads %q, want %q", row.reason, row.off, got, row.want)
		}
	}
	actor.offSnap.Store(false)
	actor.setWorkstationUp()
	if got := actor.WakeFailure(); got != WakeFailureCrashed {
		t.Errorf("up again reads %q, want the last try's %q", got, WakeFailureCrashed)
	}
}
