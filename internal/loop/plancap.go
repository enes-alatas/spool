package loop

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// DefaultPlanCapPercent is each window's threshold until the operator sets
// one: the cap is on out of the box (ADR-0047).
const DefaultPlanCapPercent = 90

// The plan's limit windows, as the cap names them.
const (
	PlanWindowFiveHour = "five_hour"
	PlanWindowSevenDay = "seven_day"
)

// Cap is the plan cap as judged at one moment: the windows at or over their
// thresholds, the latest of their resets, and how long a Resume now holds it
// off.
type Cap struct {
	Windows      []string
	Until        int64
	ResumedUntil int64
}

// Holds reports whether the cap stops the loops at the time now. A Resume
// now holds off a cap whose windows all reset by the time it named, and no
// later one: a window that goes over and resets after that caps again.
func (planCap Cap) Holds(now int64) bool {
	return len(planCap.Windows) > 0 && now < planCap.Until && planCap.ResumedUntil < planCap.Until
}

// PlanCap judges the plan cap for the whole fleet: any Claude login over a
// threshold caps every loop, on whichever login, until the cap works per
// login (#1075). It keeps what the judgment rests on in memory, so an actor
// can ask on every message and tick without a store read, and re-reads it
// whenever one of them changes.
type PlanCap struct {
	settings store.SettingsStore
	logins   store.ClaudeLoginStore
	log      *slog.Logger
	changed  chan struct{} // wakes Run to judge again; holds at most one

	mu           sync.Mutex
	usages       []LoginPlanUsage
	fiveHour     int
	sevenDay     int
	resumedUntil int64
	onChange     func()
}

// NewPlanCap returns a cap that reads its thresholds and any Resume now
// from settings, and each login's plan usage where PlanUsage keeps it.
// Nothing is capped until Refresh first reads them.
func NewPlanCap(settings store.SettingsStore, logins store.ClaudeLoginStore, log *slog.Logger) *PlanCap {
	if log == nil {
		log = slog.Default()
	}
	return &PlanCap{settings: settings, logins: logins, log: log, changed: make(chan struct{}, 1)}
}

// OnChange sets what Run calls each time the cap starts or stops holding.
// It is called from Run's goroutine.
func (planCap *PlanCap) OnChange(notify func()) {
	planCap.mu.Lock()
	defer planCap.mu.Unlock()
	planCap.onChange = notify
}

// Refresh re-reads the thresholds, every login's plan usage and the Resume
// now hold, and has Run judge the cap again. A read that fails keeps what
// was last read: the cap neither appears nor lifts on a store error.
func (planCap *PlanCap) Refresh(ctx context.Context) error {
	usages, err := PlanUsages(ctx, planCap.settings, planCap.logins)
	if err != nil {
		return err
	}
	resumedUntil, err := planCapResumedUntil(ctx, planCap.settings)
	if err != nil {
		return err
	}
	fiveHour, sevenDay := PlanCapThresholds(ctx, planCap.settings, planCap.log)
	planCap.mu.Lock()
	planCap.usages, planCap.fiveHour, planCap.sevenDay, planCap.resumedUntil = usages, fiveHour, sevenDay, resumedUntil
	planCap.mu.Unlock()
	planCap.signal()
	return nil
}

// At judges the cap at the time now.
func (planCap *PlanCap) At(now int64) Cap {
	planCap.mu.Lock()
	defer planCap.mu.Unlock()
	return fleetCapOf(planCap.usages, planCap.fiveHour, planCap.sevenDay, planCap.resumedUntil, now)
}

// Holds reports whether the cap stops the loops at the time now.
func (planCap *PlanCap) Holds(now int64) bool { return planCap.At(now).Holds(now) }

// Thresholds returns the thresholds the cap is judged by, 0 for a window
// whose cap is off.
func (planCap *PlanCap) Thresholds() (fiveHour, sevenDay int) {
	planCap.mu.Lock()
	defer planCap.mu.Unlock()
	return planCap.fiveHour, planCap.sevenDay
}

// Resume holds the cap off until its windows have all reset, without
// changing the thresholds. With nothing capped it changes nothing. The hold
// is stored, so a restart keeps it.
func (planCap *PlanCap) Resume(ctx context.Context, now int64) error {
	planCap.mu.Lock()
	current := fleetCapOf(planCap.usages, planCap.fiveHour, planCap.sevenDay, planCap.resumedUntil, now)
	if !current.Holds(now) {
		planCap.mu.Unlock()
		return nil
	}
	if err := planCap.settings.Set(ctx, store.SettingPlanCapResumedUntil, strconv.FormatInt(current.Until, 10)); err != nil {
		planCap.mu.Unlock()
		return err
	}
	planCap.resumedUntil = current.Until
	planCap.mu.Unlock()
	planCap.signal()
	return nil
}

// Run judges the cap whenever Refresh or Resume changes what it rests on,
// and when a holding cap's windows reset, and tells OnChange each time the
// cap starts or stops holding. It returns when ctx is done.
func (planCap *PlanCap) Run(ctx context.Context) {
	holding := false
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		now := time.Now().UnixMilli()
		current := planCap.At(now)
		if holds := current.Holds(now); holds != holding {
			holding = holds
			planCap.mu.Lock()
			notify := planCap.onChange
			planCap.mu.Unlock()
			if notify != nil {
				notify()
			}
		}
		timer.Stop()
		if holding {
			timer.Reset(time.Duration(current.Until-now) * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-planCap.changed:
		case <-timer.C:
		}
	}
}

func (planCap *PlanCap) signal() {
	select {
	case planCap.changed <- struct{}{}:
	default: // a judgment is already due, and it reads the newest state
	}
}

// fleetCapOf judges the cap on every login's usage at the time now: the
// windows any login is over, and the latest of those logins' resets, so
// it holds until every login over has reset.
func fleetCapOf(usages []LoginPlanUsage, fiveHour, sevenDay int, resumedUntil, now int64) Cap {
	fleet := Cap{ResumedUntil: resumedUntil}
	for _, usage := range usages {
		login := capOf(usage.Usage, fiveHour, sevenDay, resumedUntil, now)
		for _, window := range login.Windows {
			if !slices.Contains(fleet.Windows, window) {
				fleet.Windows = append(fleet.Windows, window)
			}
		}
		fleet.Until = max(fleet.Until, login.Until)
	}
	return fleet
}

// capOf judges the cap on usage at the time now. A window counts only while
// it is observed and not yet reset, so usage that is unknown never caps.
func capOf(usage store.PlanUsageRecord, fiveHour, sevenDay int, resumedUntil, now int64) Cap {
	planCap := Cap{ResumedUntil: resumedUntil}
	for _, window := range []struct {
		name      string
		observed  *store.PlanUsageWindow
		threshold int
	}{
		{PlanWindowFiveHour, usage.FiveHour, fiveHour},
		{PlanWindowSevenDay, usage.SevenDay, sevenDay},
	} {
		if window.threshold == 0 || !unexpired(window.observed, now) ||
			window.observed.Utilization*100 < float64(window.threshold) {
			continue
		}
		planCap.Windows = append(planCap.Windows, window.name)
		planCap.Until = max(planCap.Until, window.observed.ResetsAt)
	}
	return planCap
}

// ValidPlanCapPercent reports whether percent is a usable threshold: 1 to
// 100, or 0 for off.
func ValidPlanCapPercent(percent int) bool { return percent >= 0 && percent <= 100 }

// PlanCapThresholds reads the two thresholds, each the default while unset
// or unusable.
func PlanCapThresholds(ctx context.Context, settings store.SettingsStore, log *slog.Logger) (fiveHour, sevenDay int) {
	if log == nil {
		log = slog.Default()
	}
	return planCapPercent(ctx, settings, store.SettingPlanCapFiveHourPercent, log),
		planCapPercent(ctx, settings, store.SettingPlanCapSevenDayPercent, log)
}

func planCapPercent(ctx context.Context, settings store.SettingsStore, key string, log *slog.Logger) int {
	raw, err := settings.Get(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return DefaultPlanCapPercent
	}
	if err != nil {
		log.Warn("plan cap threshold unreadable; using default", "key", key, "default", DefaultPlanCapPercent, "err", err)
		return DefaultPlanCapPercent
	}
	percent, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || !ValidPlanCapPercent(percent) {
		log.Warn("stored plan cap threshold is not a percentage; using default",
			"key", key, "value", raw, "default", DefaultPlanCapPercent)
		return DefaultPlanCapPercent
	}
	return percent
}

// planCapResumedUntil reads when the last Resume now stops holding the cap
// off, 0 when there has been none.
func planCapResumedUntil(ctx context.Context, settings store.SettingsStore) (int64, error) {
	raw, err := settings.Get(ctx, store.SettingPlanCapResumedUntil)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}
