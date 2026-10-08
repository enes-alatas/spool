package loop

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

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
// since the observation says nothing about it.
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
	if observed.FiveHour == nil && unexpired(last.FiveHour, observed.At) {
		observed.FiveHour = last.FiveHour
	}
	if observed.SevenDay == nil && unexpired(last.SevenDay, observed.At) {
		observed.SevenDay = last.SevenDay
	}
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
		FiveHour: planWindowFrom(info.UnifiedWindows.FiveHour),
		SevenDay: planWindowFrom(info.UnifiedWindows.SevenDay),
		At:       at,
		Source:   store.PlanUsageSourceStream,
	}
	return record, record.FiveHour != nil || record.SevenDay != nil
}

func planWindowFrom(window *claude.UnifiedWindow) *store.PlanUsageWindow {
	if window == nil {
		return nil
	}
	return &store.PlanUsageWindow{Utilization: window.Utilization, ResetsAt: window.ResetsAt * 1000}
}
