package loop

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/store"
)

// cloudUsageEvery is how often, at most, a login's plan usage is read for
// its cloud loops, and how fresh a reading its contained loops' stream
// gave must be for the read to be skipped (#1285).
const cloudUsageEvery = 5 * time.Minute

// cloudUsageWarnEvery is how often, at most, a login's failing read is
// logged.
const cloudUsageWarnEvery = time.Hour

// ReadPlanUsage reads the plan usage of the Claude login called login, ""
// for the default, by running the CLI's /usage on it: nil windows, and no
// error, for a login whose usage can't be read that way, a sign-in
// without the scope. The cloud runtime does it.
type ReadPlanUsage func(ctx context.Context, login string) (*claude.UnifiedWindows, error)

// CloudUsage reads a login's plan usage when one of its cloud loops ends a
// turn. A cloud turn's transcript carries no rate-limit event (ADR-0059),
// so without it a login whose loops all run in the cloud would never
// report, and its plan cap could never act (#1285). It reads at most once
// per cloudUsageEvery for each login, and not at all while that login's
// contained loops keep its usage fresh. A reading is kept as a stream
// observation is: it can raise a window's usage, never lower it or move
// its reset.
type CloudUsage struct {
	read    ReadPlanUsage
	usage   *PlanUsage
	planCap *PlanCap
	log     *slog.Logger

	mu       sync.Mutex
	lastRead map[string]time.Time
	lastWarn map[string]time.Time
}

// NewCloudUsage returns the reader that keeps what read returns in usage
// and has planCap judge it.
func NewCloudUsage(read ReadPlanUsage, usage *PlanUsage, planCap *PlanCap, log *slog.Logger) *CloudUsage {
	return &CloudUsage{read: read, usage: usage, planCap: planCap, log: log,
		lastRead: map[string]time.Time{}, lastWarn: map[string]time.Time{}}
}

// TurnEnded is told a cloud loop on the login called login ended a turn,
// and reads that login's usage in the background, when it is due.
func (cloudUsage *CloudUsage) TurnEnded(login string) {
	go cloudUsage.Read(context.Background(), login, time.Now())
}

// Read reads the login's usage, observed at the time at, unless it was
// read within cloudUsageEvery of it or its stored usage is a stream
// reading that recent, and keeps it. It reports whether it read.
func (cloudUsage *CloudUsage) Read(ctx context.Context, login string, at time.Time) bool {
	if isDefaultLogin(login) {
		login = store.DefaultClaudeLogin
	}
	if !cloudUsage.due(ctx, login, at) {
		return false
	}
	windows, err := cloudUsage.read(ctx, login)
	if err != nil {
		cloudUsage.warn(login, at, err)
		return true
	}
	observed, ok := planUsageFrom(&claude.RateLimitInfo{UnifiedWindows: windows}, at.UnixMilli())
	if !ok {
		return true
	}
	observed.Source = store.PlanUsageSourceUsageCommand
	if err := cloudUsage.usage.Observe(ctx, login, observed); err != nil {
		cloudUsage.log.Error("plan usage", "login", login, "err", err)
		return true
	}
	if cloudUsage.planCap != nil {
		if err := cloudUsage.planCap.Refresh(ctx); err != nil {
			cloudUsage.log.Error("plan cap", "err", err)
		}
	}
	return true
}

// due reports whether the login's usage is to be read at the time at, and
// if so marks it read then, so a turn ending meanwhile doesn't read it
// again.
func (cloudUsage *CloudUsage) due(ctx context.Context, login string, at time.Time) bool {
	cloudUsage.mu.Lock()
	defer cloudUsage.mu.Unlock()
	if last, ok := cloudUsage.lastRead[login]; ok && at.Sub(last) < cloudUsageEvery {
		return false
	}
	stored, err := PlanUsageOf(ctx, cloudUsage.usage.settings, cloudUsage.usage.logins, login)
	if err != nil {
		// a login removed meanwhile, or a store that can't be read: the
		// next turn tries again
		return false
	}
	if stored.Source == store.PlanUsageSourceStream && at.Sub(time.UnixMilli(stored.At)) < cloudUsageEvery {
		return false
	}
	cloudUsage.lastRead[login] = at
	return true
}

// warn logs a failed read, once per cloudUsageWarnEvery for each login.
func (cloudUsage *CloudUsage) warn(login string, at time.Time, err error) {
	cloudUsage.mu.Lock()
	last, warned := cloudUsage.lastWarn[login]
	quiet := warned && at.Sub(last) < cloudUsageWarnEvery
	if !quiet {
		cloudUsage.lastWarn[login] = at
	}
	cloudUsage.mu.Unlock()
	if !quiet {
		cloudUsage.log.Warn("plan usage read failed; the login's last reading stands", "login", login, "err", err)
	}
}
