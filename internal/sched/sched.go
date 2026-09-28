// Package sched fires loop ticks from a DB-backed schedule, surviving
// restarts. The store's next_tick_at is the single source of truth.
package sched

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
)

// recheckInterval is the longest Run waits before it reads the schedule
// against the wall clock again. A timer counts monotonic time, which stops
// while the machine is suspended, and next_tick_at is wall time: waiting on
// one timer for the earliest tick let a tick that came due during a suspend
// fire only after the timer had also counted the time the suspend took from
// it, hours late. fireDue decides what is due by the wall clock, so waking
// this often bounds the catch-up after a resume to one interval.
const recheckInterval = 30 * time.Second

// lateTickThreshold is how far past its next_tick_at a tick may fire before
// it is logged as late. Recheck granularity alone never gets near it, so a
// late tick means the process did not run for a while: a suspended machine,
// or a scheduler that stalled.
const lateTickThreshold = 2 * recheckInterval

type Ticker interface {
	Tick(loopID string) bool
}

type Scheduler struct {
	store  store.Store
	bus    *bus.Bus
	ticker Ticker
	log    *slog.Logger
	poke   chan struct{}
}

func New(st store.Store, publisher *bus.Bus, ticker Ticker, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{store: st, bus: publisher, ticker: ticker, log: log, poke: make(chan struct{}, 1)}
}

// Run blocks until ctx is done. On start, overdue ticks are rescheduled with
// 0–60s jitter so a fleet doesn't spawn all at once after a restart.
func (scheduler *Scheduler) Run(ctx context.Context) {
	entries, err := scheduler.store.Schedule().All(ctx)
	if err != nil {
		scheduler.log.Error("schedule load", "err", err)
	}
	nowMS := time.Now().UnixMilli()
	for _, entry := range entries {
		if entry.NextTickAt != 0 && entry.NextTickAt <= nowMS {
			jitter := int64(rand.Intn(60_000))
			_ = scheduler.store.Schedule().Set(ctx, entry.LoopID, nowMS+jitter)
		}
	}

	for {
		wait := recheckInterval
		if next := scheduler.earliest(ctx); next != 0 {
			wait = min(wait, max(time.Until(time.UnixMilli(next)), 0))
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-scheduler.poke:
			timer.Stop()
		case <-timer.C:
			scheduler.fireDue(ctx)
		}
	}
}

// Poke re-evaluates the schedule (call after any next_tick_at change).
func (scheduler *Scheduler) Poke() {
	select {
	case scheduler.poke <- struct{}{}:
	default:
	}
}

// ScheduleAfterTurn sets the next tick after a completed turn: trailer wins
// (clamped), otherwise the loop's interval. Publishes a schedule bus item.
func (scheduler *Scheduler) ScheduleAfterTurn(loopRecord *store.Loop, trailer time.Duration, hasTrailer bool) {
	delay := time.Duration(loopRecord.TickIntervalSec) * time.Second
	if hasTrailer {
		min := time.Duration(loopRecord.MinWakeSec) * time.Second
		max := time.Duration(loopRecord.MaxWakeSec) * time.Second
		delay = clamp(trailer, min, max)
	}
	at := time.Now().Add(delay).UnixMilli()
	if err := scheduler.store.Schedule().Set(context.Background(), loopRecord.ID, at); err != nil {
		scheduler.log.Error("schedule set", "err", err)
		return
	}
	scheduler.publish(loopRecord.ID, at)
	scheduler.Poke()
}

// Suspend clears a loop's tick (pause). Resume schedules a near-term tick.
func (scheduler *Scheduler) Suspend(loopID string) {
	_ = scheduler.store.Schedule().Set(context.Background(), loopID, 0)
	scheduler.publish(loopID, 0)
	scheduler.Poke()
}

func (scheduler *Scheduler) Resume(loopID string) {
	at := time.Now().Add(time.Duration(2+rand.Intn(5)) * time.Second).UnixMilli()
	_ = scheduler.store.Schedule().Set(context.Background(), loopID, at)
	scheduler.publish(loopID, at)
	scheduler.Poke()
}

// ScheduleNow requests an immediate tick (manual wake).
func (scheduler *Scheduler) ScheduleNow(loopID string) {
	at := time.Now().UnixMilli()
	_ = scheduler.store.Schedule().Set(context.Background(), loopID, at)
	scheduler.publish(loopID, at)
	scheduler.Poke()
}

func (scheduler *Scheduler) earliest(ctx context.Context) int64 {
	entries, err := scheduler.store.Schedule().All(ctx)
	if err != nil {
		scheduler.log.Error("schedule load", "err", err)
		return time.Now().Add(30 * time.Second).UnixMilli()
	}
	var min int64
	for _, entry := range entries {
		if entry.NextTickAt == 0 {
			continue
		}
		if min == 0 || entry.NextTickAt < min {
			min = entry.NextTickAt
		}
	}
	return min
}

func (scheduler *Scheduler) fireDue(ctx context.Context) {
	entries, err := scheduler.store.Schedule().All(ctx)
	if err != nil {
		return
	}
	nowMS := time.Now().UnixMilli()
	for _, entry := range entries {
		if entry.NextTickAt == 0 || entry.NextTickAt > nowMS {
			continue
		}
		if late := time.Duration(nowMS-entry.NextTickAt) * time.Millisecond; late > lateTickThreshold {
			scheduler.log.Warn("tick fired late", "loop_id", entry.LoopID, "late", late.Round(time.Second))
		}
		// Clear before firing; the runtime's OnTurnDone (or the busy-skip
		// below) sets the next one.
		if scheduler.ticker.Tick(entry.LoopID) {
			_ = scheduler.store.Schedule().SetLastTick(ctx, entry.LoopID, nowMS)
			// If the loop was busy the tick was ignored; ensure a future tick
			// exists either way. OnTurnDone will overwrite with a better time.
			loopRecord, err := scheduler.store.Loops().Get(ctx, entry.LoopID)
			if err == nil {
				at := nowMS + int64(loopRecord.TickIntervalSec)*1000
				_ = scheduler.store.Schedule().Set(ctx, entry.LoopID, at)
				scheduler.publish(entry.LoopID, at)
			}
		} else {
			// unknown runtime (deleted?): drop the schedule row
			_ = scheduler.store.Schedule().Delete(ctx, entry.LoopID)
		}
	}
}

func (scheduler *Scheduler) publish(loopID string, at int64) {
	scheduler.bus.Publish(bus.Item{Kind: bus.KindSchedule, LoopID: loopID, Payload: map[string]any{
		"loop_id": loopID, "next_tick_at": at,
	}})
}

func clamp(delay, min, max time.Duration) time.Duration {
	if delay < min {
		return min
	}
	if delay > max {
		return max
	}
	return delay
}
