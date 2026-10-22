package loop

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/store"
)

// PlanUsage keeps the newest plan usage any loop observed. Every loop runs on
// the operator's one login, so whichever loop spoke to the API last has the
// fleet's freshest number.
type PlanUsage struct {
	settings store.SettingsStore

	mu sync.Mutex // orders observations, so an older one never overwrites a newer
}

// NewPlanUsage returns the keeper that stores its record in settings.
func NewPlanUsage(settings store.SettingsStore) *PlanUsage {
	return &PlanUsage{settings: settings}
}

// Observe keeps observed unless the stored record is newer. A window the
// observation does not carry keeps its last value until that window resets,
// since the observation says nothing about it. Within a window that has not
// reset, an observation can only raise the usage and never moves the reset:
// the stream it came from is within its loop's reach, and one loop must not
// lift or stretch the cap on the whole fleet (ADR-0047).
func (usage *PlanUsage) Observe(ctx context.Context, observed store.PlanUsageRecord) error {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	last, err := LastPlanUsage(ctx, usage.settings)
	if err != nil {
		return err
	}
	if last.At > observed.At {
		return nil
	}
	observed.FiveHour = withinWindow(last.FiveHour, observed.FiveHour, observed.At)
	observed.SevenDay = withinWindow(last.SevenDay, observed.SevenDay, observed.At)
	raw, err := json.Marshal(observed)
	if err != nil {
		return err
	}
	return usage.settings.Set(ctx, store.SettingPlanUsage, string(raw))
}

// LastPlanUsage reads the stored record, the zero record when there is none.
func LastPlanUsage(ctx context.Context, settings store.SettingsStore) (store.PlanUsageRecord, error) {
	var record store.PlanUsageRecord
	raw, err := settings.Get(ctx, store.SettingPlanUsage)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	err = json.Unmarshal([]byte(raw), &record)
	return record, err
}

// withinWindow merges an observed window into the last one kept at the time
// at. While the last has not reset, it stands: an observation that is
// missing, or reads lower, leaves its usage, and none moves its reset.
// Once it has reset, the observation is the window's value.
func withinWindow(last, observed *store.PlanUsageWindow, at int64) *store.PlanUsageWindow {
	if !unexpired(last, at) {
		return observed
	}
	if observed == nil || observed.Utilization <= last.Utilization {
		return last
	}
	return &store.PlanUsageWindow{Utilization: observed.Utilization, ResetsAt: last.ResetsAt}
}

// unexpired reports whether window still holds at the time at: it exists and
// has not reset yet.
func unexpired(window *store.PlanUsageWindow, at int64) bool {
	return window != nil && window.ResetsAt > at
}

// planUsageFrom reads a rate-limit event observed at the time at as plan
// usage. It reports false for an event that carries no window, which is what
// an older CLI, or a login with no plan limits, sends.
func planUsageFrom(info *claude.RateLimitInfo, at int64) (store.PlanUsageRecord, bool) {
	if info == nil || info.UnifiedWindows == nil {
		return store.PlanUsageRecord{}, false
	}
	record := store.PlanUsageRecord{
		FiveHour: planWindowFrom(info.UnifiedWindows.FiveHour, at, 5*time.Hour),
		SevenDay: planWindowFrom(info.UnifiedWindows.SevenDay, at, 7*24*time.Hour),
		At:       at,
		Source:   store.PlanUsageSourceStream,
	}
	return record, record.FiveHour != nil || record.SevenDay != nil
}

// resetSlack is how far past a window's span its reset may read, for a hub
// clock behind the API's.
const resetSlack = 15 * time.Minute

// planWindowFrom reads one window observed at the time at, or nil for one
// that can't be a window of that span: a negative usage, a reset already
// past, or one further off than the span itself, which would hold the cap
// past any real reset.
func planWindowFrom(window *claude.UnifiedWindow, at int64, span time.Duration) *store.PlanUsageWindow {
	if window == nil || window.Utilization < 0 || window.ResetsAt <= at/1000 ||
		window.ResetsAt > (at+(span+resetSlack).Milliseconds())/1000 {
		return nil
	}
	return &store.PlanUsageWindow{Utilization: window.Utilization, ResetsAt: window.ResetsAt * 1000}
}
