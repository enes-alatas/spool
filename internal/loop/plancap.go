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

// PlanCap judges the plan cap of each Claude login: a login over a
// threshold caps only the loops on it, until that login's until (ADR-0060).
// It keeps what the judgment rests on in memory, so an actor can ask on
// every message and tick without a store read, and re-reads it whenever one
// of them changes.
type PlanCap struct {
	settings store.SettingsStore
	logins   store.ClaudeLoginStore
	log      *slog.Logger
	changed  chan struct{} // wakes Run to judge again; holds at most one

	mu       sync.Mutex
	held     map[string]loginCapState // by login name, the default as DefaultClaudeLogin
	fiveHour int
	sevenDay int
	onChange func()
}

// loginCapState is what one login's cap is judged on: its plan usage and
// when its last Resume now stops holding the cap off.
type loginCapState struct {
	usage        store.PlanUsageRecord
	resumedUntil int64
}

// NewPlanCap returns a cap that reads its thresholds from settings, and
// each login's plan usage and Resume now hold where the login keeps them:
// the default's in settings, another's on the login. Nothing is capped
// until Refresh first reads them.
func NewPlanCap(settings store.SettingsStore, logins store.ClaudeLoginStore, log *slog.Logger) *PlanCap {
	if log == nil {
		log = slog.Default()
	}
	return &PlanCap{settings: settings, logins: logins, log: log, changed: make(chan struct{}, 1)}
}

// OnChange sets what Run calls each time a login's cap starts or stops
// holding. It is called from Run's goroutine.
func (planCap *PlanCap) OnChange(notify func()) {
	planCap.mu.Lock()
	defer planCap.mu.Unlock()
	planCap.onChange = notify
}

// Refresh re-reads the thresholds and every login's plan usage and Resume
// now hold, and has Run judge the caps again. A read that fails keeps what
// was last read: no cap appears or lifts on a store error.
func (planCap *PlanCap) Refresh(ctx context.Context) error {
	defaultUsage, err := LastPlanUsage(ctx, planCap.settings)
	if err != nil {
		return err
	}
	defaultResumed, err := planCapResumedUntil(ctx, planCap.settings)
	if err != nil {
		return err
	}
	logins, err := planCap.logins.List(ctx)
	if err != nil {
		return err
	}
	held := map[string]loginCapState{store.DefaultClaudeLogin: {defaultUsage, defaultResumed}}
	for _, login := range logins {
		if login.Name != store.DefaultClaudeLogin {
			held[login.Name] = loginCapState{login.PlanUsage, login.PlanCapResumedUntil}
		}
	}
	fiveHour, sevenDay := PlanCapThresholds(ctx, planCap.settings, planCap.log)
	planCap.mu.Lock()
	planCap.held, planCap.fiveHour, planCap.sevenDay = held, fiveHour, sevenDay
	planCap.mu.Unlock()
	planCap.signal()
	return nil
}

// At judges, at the time now, the cap of the login a loop names as login:
// "" for the default, which cloud loops follow too. A login the hub
// doesn't hold has no usage, so it is never capped.
func (planCap *PlanCap) At(login string, now int64) Cap {
	planCap.mu.Lock()
	defer planCap.mu.Unlock()
	return planCap.at(loginKey(login), now)
}

func (planCap *PlanCap) at(name string, now int64) Cap {
	state := planCap.held[name]
	return capOf(state.usage, planCap.fiveHour, planCap.sevenDay, state.resumedUntil, now)
}

// Holds reports whether the cap of the login a loop names as login stops
// that loop at the time now.
func (planCap *PlanCap) Holds(login string, now int64) bool {
	return planCap.At(login, now).Holds(now)
}

// Thresholds returns the thresholds every login's cap is judged by, 0 for
// a window whose cap is off.
func (planCap *PlanCap) Thresholds() (fiveHour, sevenDay int) {
	planCap.mu.Lock()
	defer planCap.mu.Unlock()
	return planCap.fiveHour, planCap.sevenDay
}

// Resume holds the cap of the login a loop names as login off until its
// windows have all reset, without changing the thresholds. With that login
// not capped it changes nothing. The hold is stored, so a restart keeps it.
func (planCap *PlanCap) Resume(ctx context.Context, login string, now int64) error {
	name := loginKey(login)
	planCap.mu.Lock()
	current := planCap.at(name, now)
	if !current.Holds(now) {
		planCap.mu.Unlock()
		return nil
	}
	var err error
	if name == store.DefaultClaudeLogin {
		err = planCap.settings.Set(ctx, store.SettingPlanCapResumedUntil, strconv.FormatInt(current.Until, 10))
	} else {
		err = planCap.logins.SetPlanCapResumedUntil(ctx, name, current.Until)
	}
	if err != nil {
		planCap.mu.Unlock()
		return err
	}
	state := planCap.held[name]
	state.resumedUntil = current.Until
	planCap.held[name] = state
	planCap.mu.Unlock()
	planCap.signal()
	return nil
}

// Run judges the caps whenever Refresh or Resume changes what they rest
// on, and when a holding cap's windows reset, and tells OnChange each time
// a login's cap starts or stops holding. It returns when ctx is done.
func (planCap *PlanCap) Run(ctx context.Context) {
	var holding []string
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		now := time.Now().UnixMilli()
		current, wake := planCap.holding(now)
		if !slices.Equal(current, holding) {
			holding = current
			planCap.mu.Lock()
			notify := planCap.onChange
			planCap.mu.Unlock()
			if notify != nil {
				notify()
			}
		}
		timer.Stop()
		if len(holding) > 0 {
			timer.Reset(time.Duration(wake-now) * time.Millisecond)
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

// holding is the logins whose cap holds at the time now, by name, and the
// earliest of their untils, when the next of them lifts.
func (planCap *PlanCap) holding(now int64) (names []string, wake int64) {
	planCap.mu.Lock()
	defer planCap.mu.Unlock()
	for name := range planCap.held {
		if current := planCap.at(name, now); current.Holds(now) {
			names = append(names, name)
			if wake == 0 || current.Until < wake {
				wake = current.Until
			}
		}
	}
	slices.Sort(names)
	return names, wake
}

func (planCap *PlanCap) signal() {
	select {
	case planCap.changed <- struct{}{}:
	default: // a judgment is already due, and it reads the newest state
	}
}

// loginKey is the name of the Claude login a loop's claude_login stands
// for: "" is the default.
func loginKey(login string) string {
	if login == "" {
		return store.DefaultClaudeLogin
	}
	return login
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
