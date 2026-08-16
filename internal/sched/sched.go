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

func New(st store.Store, b *bus.Bus, t Ticker, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{store: st, bus: b, ticker: t, log: log, poke: make(chan struct{}, 1)}
}

// Run blocks until ctx is done. On start, overdue ticks are rescheduled with
// 0–60s jitter so a fleet doesn't spawn all at once after a restart.
func (s *Scheduler) Run(ctx context.Context) {
	entries, err := s.store.Schedule().All(ctx)
	if err != nil {
		s.log.Error("schedule load", "err", err)
	}
	nowMS := time.Now().UnixMilli()
	for _, e := range entries {
		if e.NextTickAt != 0 && e.NextTickAt <= nowMS {
			jitter := int64(rand.Intn(60_000))
			_ = s.store.Schedule().Set(ctx, e.LoopID, nowMS+jitter)
		}
	}

	for {
		next := s.earliest(ctx)
		var timer *time.Timer
		var fire <-chan time.Time
		if next != 0 {
			d := time.Until(time.UnixMilli(next))
			if d < 0 {
				d = 0
			}
			timer = time.NewTimer(d)
			fire = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.poke:
			if timer != nil {
				timer.Stop()
			}
		case <-fire:
			s.fireDue(ctx)
		}
	}
}

// Poke re-evaluates the schedule (call after any next_tick_at change).
func (s *Scheduler) Poke() {
	select {
	case s.poke <- struct{}{}:
	default:
	}
}

// ScheduleAfterTurn sets the next tick after a completed turn: trailer wins
// (clamped), otherwise the loop's interval. Publishes a schedule bus item.
func (s *Scheduler) ScheduleAfterTurn(l *store.Loop, trailer time.Duration, hasTrailer bool) {
	d := time.Duration(l.TickIntervalSec) * time.Second
	if hasTrailer {
		min := time.Duration(l.MinWakeSec) * time.Second
		max := time.Duration(l.MaxWakeSec) * time.Second
		d = clamp(trailer, min, max)
	}
	at := time.Now().Add(d).UnixMilli()
	if err := s.store.Schedule().Set(context.Background(), l.ID, at); err != nil {
		s.log.Error("schedule set", "err", err)
		return
	}
	s.publish(l.ID, at)
	s.Poke()
}

// Suspend clears a loop's tick (pause). Resume schedules a near-term tick.
func (s *Scheduler) Suspend(loopID string) {
	_ = s.store.Schedule().Set(context.Background(), loopID, 0)
	s.publish(loopID, 0)
	s.Poke()
}

func (s *Scheduler) Resume(loopID string) {
	at := time.Now().Add(time.Duration(2+rand.Intn(5)) * time.Second).UnixMilli()
	_ = s.store.Schedule().Set(context.Background(), loopID, at)
	s.publish(loopID, at)
	s.Poke()
}

// ScheduleNow requests an immediate tick (manual wake).
func (s *Scheduler) ScheduleNow(loopID string) {
	at := time.Now().UnixMilli()
	_ = s.store.Schedule().Set(context.Background(), loopID, at)
	s.publish(loopID, at)
	s.Poke()
}

func (s *Scheduler) earliest(ctx context.Context) int64 {
	entries, err := s.store.Schedule().All(ctx)
	if err != nil {
		s.log.Error("schedule load", "err", err)
		return time.Now().Add(30 * time.Second).UnixMilli()
	}
	var min int64
	for _, e := range entries {
		if e.NextTickAt == 0 {
			continue
		}
		if min == 0 || e.NextTickAt < min {
			min = e.NextTickAt
		}
	}
	return min
}

func (s *Scheduler) fireDue(ctx context.Context) {
	entries, err := s.store.Schedule().All(ctx)
	if err != nil {
		return
	}
	nowMS := time.Now().UnixMilli()
	for _, e := range entries {
		if e.NextTickAt == 0 || e.NextTickAt > nowMS {
			continue
		}
		// Clear before firing; the runtime's OnTurnDone (or the busy-skip
		// below) sets the next one.
		if s.ticker.Tick(e.LoopID) {
			_ = s.store.Schedule().SetLastTick(ctx, e.LoopID, nowMS)
			// If the loop was busy the tick was ignored; ensure a future tick
			// exists either way. OnTurnDone will overwrite with a better time.
			l, err := s.store.Loops().Get(ctx, e.LoopID)
			if err == nil {
				at := nowMS + int64(l.TickIntervalSec)*1000
				_ = s.store.Schedule().Set(ctx, e.LoopID, at)
				s.publish(e.LoopID, at)
			}
		} else {
			// unknown runtime (deleted?): drop the schedule row
			_ = s.store.Schedule().Delete(ctx, e.LoopID)
		}
	}
}

func (s *Scheduler) publish(loopID string, at int64) {
	s.bus.Publish(bus.Item{Kind: bus.KindSchedule, LoopID: loopID, Payload: map[string]any{
		"loop_id": loopID, "next_tick_at": at,
	}})
}

func clamp(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}
