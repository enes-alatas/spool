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
	if log == nil {
		log = slog.Default()
	}
	raw, err := settings.Get(ctx, store.SettingHealthErrorTurns)
	if errors.Is(err, store.ErrNotFound) {
		return DefaultHealthErrorTurns
	}
	if err != nil {
		log.Warn("health error-turn threshold unreadable; using default", "default", DefaultHealthErrorTurns, "err", err)
		return DefaultHealthErrorTurns
	}
	turns, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || !ValidHealthErrorTurns(turns) {
		log.Warn("stored health error-turn threshold is not a count; using default",
			"value", raw, "default", DefaultHealthErrorTurns)
		return DefaultHealthErrorTurns
	}
	return turns
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
// and a loop still failing after as many errored turns again.
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
		actor.openProblem(store.SymptomTurnErrors, actor.firstErrorAt, actor.errorTurns)
	}
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
		actor.openProblem(store.SymptomWorkstationDown, actor.downSince, 0)
	}
}

func (actor *Actor) downGrace() time.Duration {
	if actor.deps.ProblemDownGrace > 0 {
		return actor.deps.ProblemDownGrace
	}
	return defaultDownGrace
}

// openProblem records a problem unless one is open for the symptom already,
// and only then tells the owner and the control room.
func (actor *Actor) openProblem(symptom string, since int64, turns int) {
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
	actor.problemsChanged(surface.ProblemNotice{Symptom: symptom, Opened: true, Since: since, Turns: turns})
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

// Health sums up open problems: failing while the workstation is down or a
// cloud loop's address fails its check, since the loop is not running at
// all, warning for errored turns, and ok with none.
func Health(problems []store.Problem) string {
	health := HealthOK
	for _, problem := range problems {
		if problem.Symptom == store.SymptomWorkstationDown || problem.Symptom == store.SymptomCloudAddress {
			return HealthFailing
		}
		health = HealthWarning
	}
	return health
}
