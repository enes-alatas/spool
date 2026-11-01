package loop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// The hub's health check (ADR-0053, #764). Each actor watches its own loop
// for two symptoms, a cloud loop for a third (ADR-0056), and keeps at most
// one open problem per symptom in the store. Opening and clearing one is
// told to the owner once each, as a hub notice, and shows on the loop
// record; nothing is said while it stays open.

// DefaultHealthErrorTurns is how many errored turns in a row open a
// turn_errors problem while the setting is unset.
const DefaultHealthErrorTurns = 3

// defaultDownGrace is how long a workstation stays down before it is a
// problem: past the first rungs of the crash ladder, so a blip that heals
// on its own says nothing.
const defaultDownGrace = 5 * time.Minute

// ValidHealthErrorTurns reports whether turns is a usable threshold: 1 to
// 100, or 0 for off.
func ValidHealthErrorTurns(turns int) bool { return turns >= 0 && turns <= 100 }

// HealthErrorTurns reads the turn_errors threshold, the default while unset
// or unusable.
func HealthErrorTurns(ctx context.Context, settings store.SettingsStore, log *slog.Logger) int {
	return healthSetting(ctx, settings, log, store.SettingHealthErrorTurns, DefaultHealthErrorTurns, ValidHealthErrorTurns)
}

// DefaultHealthTurnMinutes is how many minutes a turn runs before it opens
// a turn_stuck problem while the setting is unset (#1142, settled on #754).
const DefaultHealthTurnMinutes = 120

// ValidHealthTurnMinutes reports whether minutes is a usable limit: 1 to a
// day, or 0 for off.
func ValidHealthTurnMinutes(minutes int) bool { return minutes >= 0 && minutes <= 24*60 }

// HealthTurnMinutes reads the turn_stuck limit, the default while unset or
// unusable.
func HealthTurnMinutes(ctx context.Context, settings store.SettingsStore, log *slog.Logger) int {
	return healthSetting(ctx, settings, log, store.SettingHealthTurnMinutes, DefaultHealthTurnMinutes, ValidHealthTurnMinutes)
}

// healthSetting reads the health check's setting key as a whole number,
// fallback while it is unset or isn't one valid takes.
func healthSetting(ctx context.Context, settings store.SettingsStore, log *slog.Logger, key string, fallback int, valid func(int) bool) int {
	if log == nil {
		log = slog.Default()
	}
	raw, err := settings.Get(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return fallback
	}
	if err != nil {
		log.Warn("health setting unreadable; using default", "setting", key, "default", fallback, "err", err)
		return fallback
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || !valid(value) {
		log.Warn("stored health setting is not a usable number; using default",
			"setting", key, "value", raw, "default", fallback)
		return fallback
	}
	return value
}

// Problems is the loop's open problems, oldest first (safe from any
// goroutine).
func (actor *Actor) Problems() []store.Problem {
	open, _ := actor.problemsSnap.Load().([]store.Problem)
	return open
}

// Acknowledge clears the loop's open problem for symptom as the operator's
// doing, and reports whether there was one. The symptom's watch starts over:
// a workstation still down is a problem again after another grace period,
// and a loop still failing after as many errored turns again. A turn past
// its limit doesn't open it again, though the loop's next turn starts a new
// limit.
func (actor *Actor) Acknowledge(symptom string) bool {
	reply := make(chan bool, 1)
	if !actor.send(cmd{kind: "acknowledge", symptom: symptom, cleared: reply}) {
		return false
	}
	select {
	case cleared := <-reply:
		return cleared
	case <-actor.stopped:
		return false
	}
}

func (actor *Actor) acknowledge(symptom string) bool {
	cleared := actor.clearProblem(symptom, store.ClearedByOperator)
	switch symptom {
	case store.SymptomTurnErrors:
		actor.errorTurns, actor.firstErrorAt = 0, 0
	case store.SymptomWorkstationDown:
		if actor.downSince != 0 {
			actor.downSince = now()
			actor.downTimer.Reset(actor.downGrace())
		}
	}
	return cleared
}

// noteTurnEnd counts errored turns in a row: the threshold's worth opens a
// turn_errors problem, dated from the first of them, and a turn that ends
// well clears it. A refused login is not counted, since the login notice
// already tells the owner of it.
func (actor *Actor) noteTurnEnd(errored bool, at int64) {
	if !errored {
		actor.errorTurns, actor.firstErrorAt = 0, 0
		actor.clearProblem(store.SymptomTurnErrors, store.ClearedByRecovery)
		return
	}
	actor.errorTurns++
	if actor.errorTurns == 1 {
		actor.firstErrorAt = at
	}
	threshold := HealthErrorTurns(context.Background(), actor.deps.Store.Settings(), actor.log())
	if threshold > 0 && actor.errorTurns >= threshold {
		actor.openProblem(surface.ProblemNotice{Symptom: store.SymptomTurnErrors, Since: actor.firstErrorAt, Turns: actor.errorTurns})
	}
}

// watchTurn starts the turn_stuck limit for a turn that just started
// (#1142). The turn is never killed for running long: the owner is told,
// and Kill is theirs to use.
func (actor *Actor) watchTurn(turn *store.Turn) {
	minutes := HealthTurnMinutes(context.Background(), actor.deps.Store.Settings(), actor.log())
	if minutes == 0 {
		return
	}
	actor.stuckTurn = turn.ID
	actor.stuckTimer.Reset(time.Duration(minutes) * time.Minute)
}

// turnLasted opens the turn_stuck problem, dated from the turn's start,
// when the limit ends with the turn it was started for still running.
func (actor *Actor) turnLasted() {
	if actor.turn == nil || actor.turn.ID != actor.stuckTurn {
		return
	}
	minutes := int(time.Duration(now()-actor.turn.StartedAt) * time.Millisecond / time.Minute)
	actor.openProblem(surface.ProblemNotice{Symptom: store.SymptomTurnStuck, Since: actor.turn.StartedAt, Minutes: minutes})
}

// unwatchTurn stops the limit when the turn ends, well or not, and clears
// its problem.
func (actor *Actor) unwatchTurn() {
	actor.stuckTurn = ""
	actor.stuckTimer.Stop()
	actor.clearProblem(store.SymptomTurnStuck, store.ClearedByRecovery)
}

// watchDown follows the workstation verdict: a workstation down for a
// reason nobody else tells the owner about starts the grace period, and
// one that is up clears its problem. A refused login has its own notice,
// and a switched-off or never-built workstation is not down for anything
// the operator must hear of, so either stops the grace period. Neither is
// up, though, so a problem already open stays open until a verdict is, or
// the operator acts: a hub restart judges a lost workstation never built,
// and the owner must not read that it is up again.
func (actor *Actor) watchDown() {
	if actor.wsDown && actor.wsReason != DownReasonUnauthenticated && actor.wsReason != DownReasonPoweredOff {
		if actor.downSince == 0 {
			actor.downSince = now()
			actor.downTimer.Reset(actor.downGrace())
		}
		return
	}
	actor.downSince = 0
	actor.downTimer.Stop()
	if actor.WorkstationHealth().Up {
		actor.clearProblem(store.SymptomWorkstationDown, store.ClearedByRecovery)
	}
}

// downLasted opens the workstation_down problem when the grace period ends
// with the workstation still down.
func (actor *Actor) downLasted() {
	if actor.downSince != 0 {
		actor.openProblem(surface.ProblemNotice{Symptom: store.SymptomWorkstationDown, Since: actor.downSince})
	}
}

func (actor *Actor) downGrace() time.Duration {
	if actor.deps.ProblemDownGrace > 0 {
		return actor.deps.ProblemDownGrace
	}
	return defaultDownGrace
}

// openProblem records a problem for the notice's symptom, dated from its
// Since, unless one is open for the symptom already, and only then tells
// the owner and the control room.
func (actor *Actor) openProblem(notice surface.ProblemNotice) {
	symptom, since := notice.Symptom, notice.Since
	opened, err := actor.deps.Store.Problems().Open(context.Background(), actor.loop.ID, symptom, since)
	if err != nil {
		actor.log().Error("open problem", "symptom", symptom, "err", err)
		return
	}
	if !opened {
		return
	}
	actor.log().Warn("problem opened", "symptom", symptom)
	actor.storeSpoolEvent("problem_opened", fmt.Sprintf(`{"symptom":%q,"since":%d}`, symptom, since))
	notice.Opened = true
	actor.problemsChanged(notice)
}

// clearProblem closes the open problem for the symptom, if there is one,
// and tells the owner and the control room; it reports whether there was.
func (actor *Actor) clearProblem(symptom, by string) bool {
	var open *store.Problem
	for _, problem := range actor.Problems() {
		if problem.Symptom == symptom {
			open = &problem
		}
	}
	if open == nil {
		return false
	}
	cleared, err := actor.deps.Store.Problems().Clear(context.Background(), actor.loop.ID, symptom, now(), by)
	if err != nil {
		actor.log().Error("clear problem", "symptom", symptom, "err", err)
		return false
	}
	if !cleared {
		return false
	}
	actor.log().Info("problem cleared", "symptom", symptom, "by", by)
	actor.storeSpoolEvent("problem_cleared", fmt.Sprintf(`{"symptom":%q,"by":%q}`, symptom, by))
	actor.problemsChanged(surface.ProblemNotice{Symptom: symptom, Since: open.Since, ByOperator: by == store.ClearedByOperator})
	return true
}

// problemsChanged re-reads the open problems for the loop record, asks the
// control room to refetch it, and hands the notice to the surfaces.
func (actor *Actor) problemsChanged(notice surface.ProblemNotice) {
	actor.loadProblems()
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: actor.loop.ID, Payload: map[string]any{
		"loop_id": actor.loop.ID, "name": actor.loop.Name, "problems_changed": true,
	}})
	notice.LoopID, notice.LoopName = actor.loop.ID, actor.loop.Name
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindLoopProblem, LoopID: actor.loop.ID, Payload: &notice})
}

func (actor *Actor) loadProblems() {
	open, err := actor.deps.Store.Problems().ListOpen(context.Background(), actor.loop.ID)
	if err != nil {
		actor.log().Error("read open problems", "err", err)
		return
	}
	actor.problemsSnap.Store(open)
}

// The health a loop's open problems add up to, as the Fleet row shows it.
const (
	HealthOK      = "ok"
	HealthWarning = "warning"
	HealthFailing = "failing"
)

// Health sums up open problems: failing while the workstation is down, a
// cloud loop's address fails its check or its setup isn't done, since the
// loop is not running at all, warning for errored turns, and ok with none.
func Health(problems []store.Problem) string {
	health := HealthOK
	for _, problem := range problems {
		if problem.Symptom == store.SymptomWorkstationDown || problem.Symptom == store.SymptomCloudAddress || problem.Symptom == store.SymptomCloudSetup {
			return HealthFailing
		}
		health = HealthWarning
	}
	return health
}
