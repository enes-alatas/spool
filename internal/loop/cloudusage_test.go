package loop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// cloudUsageFixture is a CloudUsage over a fresh store whose reads answer
// whatever answer holds, counting them.
type cloudUsageFixture struct {
	usage  *PlanUsage
	cloud  *CloudUsage
	answer atomic.Pointer[claude.UnifiedWindows]
	fail   atomic.Bool
	reads  atomic.Int32
}

func newCloudUsageFixture(t *testing.T) *cloudUsageFixture {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fixture := &cloudUsageFixture{usage: NewPlanUsage(db.Settings(), db.ClaudeLogins())}
	read := func(context.Context, string) (*claude.UnifiedWindows, error) {
		fixture.reads.Add(1)
		if fixture.fail.Load() {
			return nil, errors.New("the API answered 500")
		}
		return fixture.answer.Load(), nil
	}
	fixture.cloud = NewCloudUsage(read, fixture.usage, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return fixture
}

// stored is the default login's usage.
func (fixture *cloudUsageFixture) stored(t *testing.T) store.PlanUsageRecord {
	t.Helper()
	record, err := LastPlanUsage(context.Background(), fixture.usage.settings)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// TestCloudUsageReadsAtMostEveryFiveMinutes: a turn's end reads the
// login's usage and keeps it as the usage command's; a second within the
// interval doesn't read, and one after it does.
func TestCloudUsageReadsAtMostEveryFiveMinutes(t *testing.T) {
	fixture := newCloudUsageFixture(t)
	start := time.Now()
	resets := start.Add(4 * time.Hour).Unix()
	fixture.answer.Store(&claude.UnifiedWindows{FiveHour: &claude.UnifiedWindow{Utilization: 0.4, ResetsAt: resets}})
	ctx := context.Background()

	if !fixture.cloud.Read(ctx, "", start) {
		t.Fatal("the first turn's end didn't read")
	}
	record := fixture.stored(t)
	if record.Source != store.PlanUsageSourceUsageCommand || record.FiveHour == nil || record.FiveHour.Utilization != 0.4 ||
		record.FiveHour.ResetsAt != resets*1000 {
		t.Fatalf("kept %+v, want the usage command's 0.4", record)
	}
	if fixture.cloud.Read(ctx, "default", start.Add(4*time.Minute)) {
		t.Error("a turn ending four minutes later read again")
	}
	if !fixture.cloud.Read(ctx, "", start.Add(6*time.Minute)) || fixture.reads.Load() != 2 {
		t.Errorf("a turn ending six minutes later: %d reads, want 2", fixture.reads.Load())
	}
}

// TestCloudUsageLeavesAFreshStreamReading: a login whose contained loops
// reported within the interval isn't read; one whose last stream reading
// is older is.
func TestCloudUsageLeavesAFreshStreamReading(t *testing.T) {
	fixture := newCloudUsageFixture(t)
	start := time.Now()
	resets := start.Add(4 * time.Hour).UnixMilli()
	ctx := context.Background()
	if err := fixture.usage.Observe(ctx, "", store.PlanUsageRecord{FiveHour: &store.PlanUsageWindow{Utilization: 0.5, ResetsAt: resets},
		At: start.UnixMilli(), Source: store.PlanUsageSourceStream}); err != nil {
		t.Fatal(err)
	}
	if fixture.cloud.Read(ctx, "", start.Add(2*time.Minute)) || fixture.reads.Load() != 0 {
		t.Fatal("a login with a stream reading two minutes old was read")
	}
	fixture.answer.Store(&claude.UnifiedWindows{FiveHour: &claude.UnifiedWindow{Utilization: 0.6, ResetsAt: resets / 1000}})
	if !fixture.cloud.Read(ctx, "", start.Add(10*time.Minute)) {
		t.Fatal("a login whose stream reading is ten minutes old wasn't read")
	}
	if record := fixture.stored(t); record.Source != store.PlanUsageSourceUsageCommand || record.FiveHour.Utilization != 0.6 {
		t.Fatalf("kept %+v, want the usage command's 0.6", record)
	}
}

// TestCloudUsageNeverLowersAReading: within a window, a lower reading and a
// failed read both leave the usage kept, and a read with nothing to read
// changes nothing.
func TestCloudUsageNeverLowersAReading(t *testing.T) {
	fixture := newCloudUsageFixture(t)
	start := time.Now()
	resets := start.Add(4 * time.Hour).Unix()
	ctx := context.Background()
	fixture.answer.Store(&claude.UnifiedWindows{FiveHour: &claude.UnifiedWindow{Utilization: 0.8, ResetsAt: resets}})
	fixture.cloud.Read(ctx, "", start)

	fixture.answer.Store(&claude.UnifiedWindows{FiveHour: &claude.UnifiedWindow{Utilization: 0.1, ResetsAt: resets}})
	fixture.cloud.Read(ctx, "", start.Add(6*time.Minute))
	fixture.fail.Store(true)
	fixture.cloud.Read(ctx, "", start.Add(12*time.Minute))
	fixture.fail.Store(false)
	fixture.answer.Store(nil)
	fixture.cloud.Read(ctx, "", start.Add(18*time.Minute))
	if fixture.reads.Load() != 4 {
		t.Fatalf("%d reads, want 4", fixture.reads.Load())
	}
	if record := fixture.stored(t); record.FiveHour == nil || record.FiveHour.Utilization != 0.8 {
		t.Fatalf("kept %+v, want the 0.8 read first", record.FiveHour)
	}
}
