package loop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// The hub's health check (ADR-0053, #764). Each actor watches its own loop
// for its symptoms, a cloud loop for more (ADR-0056), save a missed wake,
// which the scheduler checks from outside every actor (#1143). A loop has
// at most one open problem per symptom in the store. Opening and clearing
// one is told to the owner once each, as a hub notice, and shows on the
// loop record; nothing is said while it stays open.

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

// DefaultHealthWakeGraceMinutes is how many minutes a wake the loop owes
// may go without a turn before it opens a wake_missed problem while the
// setting is unset (#1143, settled on #754).
const DefaultHealthWakeGraceMinutes = 10

// ValidHealthWakeGraceMinutes reports whether minutes is a usable grace: 1
// to a day, or 0 for off.
func ValidHealthWakeGraceMinutes(minutes int) bool { return minutes >= 0 && minutes <= 24*60 }

// HealthWakeGraceMinutes reads the wake_missed grace, the default while
// unset or unusable.
func HealthWakeGraceMinutes(ctx context.Context, settings store.SettingsStore, log *slog.Logger) int {
	return healthSetting(ctx, settings, log, store.SettingHealthWakeGraceMinutes, DefaultHealthWakeGraceMinutes, ValidHealthWakeGraceMinutes)
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
// A missed wake is acknowledged outside the actor, by AcknowledgeMissedWake.
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

// The wake check (#1143). A wake the loop owes, a tick that fired or a
// message delivered, must start a turn within the grace. It is owed from
// the moment it is asked, by a caller that never waits on the actor, until
// the actor has read the command; from then on, for as long as work waits
// in the inbox with no turn running and nothing holding the loop. So an
// actor that stops reading its commands is caught as well as one that
// reads them and can't start a turn. A tick the actor drops, because a
// turn is running or the loop is held, is owed no longer once it is read.
// A turn that starts after a wake was owed answers it.
//
// The problem is opened, acknowledged and cleared at a turn's start under
// wakeMu, from the scheduler's goroutine, the API's and the actor's: the
// open re-checks after it has stored the row, so it either sees the turn
// that started, or the actor's clear sees the row it opened.

// askWake sends the wake command, owed until the actor has handled it.
func (actor *Actor) askWake(command cmd) {
	actor.wakesMu.Lock()
	if actor.wakesAsked == 0 {
		actor.wakesAskedAt = now()
	}
	actor.wakesAsked++
	actor.wakesMu.Unlock()
	if !actor.send(command) {
		actor.wakeHandled()
	}
}

// wakeHandled settles one asked wake. Any still unread are owed from now,
// since when each was asked isn't kept.
func (actor *Actor) wakeHandled() {
	actor.wakesMu.Lock()
	defer actor.wakesMu.Unlock()
	actor.wakesAsked--
	actor.wakesAskedAt = 0
	if actor.wakesAsked > 0 {
		actor.wakesAskedAt = now()
	}
}

// noteOwed keeps since when the inbox has waited for a turn, after every
// command, event or timer the actor handles: work queued, no turn running,
// and nothing holding the loop. A reaction or vote that only wakes it is
// not counted, since a wake may find nothing new in it. Nor is the wait of
// the crash ladder: each of its tries is a turn, and turn_errors watches
// those, so a grace shorter than the ladder's top rung would otherwise open
// and clear the problem at every rung.
func (actor *Actor) noteOwed() {
	owed := actor.turn == nil && !actor.wsDown && actor.backoff == 0 &&
		slices.ContainsFunc(actor.inbox, func(env Envelope) bool { return !env.wakeOnly() }) &&
		!actor.holdsWork()
	switch {
	case !owed:
		actor.owedSnap.Store(0)
	case actor.owedSnap.Load() == 0:
		actor.owedSnap.Store(now())
	}
}

// wakeOwedSince is since when the loop has owed a turn, 0 when it doesn't
// (safe from any goroutine).
func (actor *Actor) wakeOwedSince() int64 {
	actor.wakesMu.Lock()
	since := actor.wakesAskedAt
	actor.wakesMu.Unlock()
	if owed := actor.owedSnap.Load(); owed != 0 && (since == 0 || owed < since) {
		since = owed
	}
	return since
}

// wakeMissed reports whether the loop has owed a wake since since, with no
// turn started from then and nothing holding it (safe from any goroutine).
func (actor *Actor) wakeMissed(since int64) bool {
	return since != 0 && actor.turnStartedAt.Load() < since && !actor.wakeHeld()
}

// wakeHeld reports, from the last published state and problems, that the
// loop is not taking turns for a reason the owner already hears of or
// chose: a running turn, which is turn_stuck's to watch, a pause, a cap, a
// workstation switched off or down, a refused model, or a cloud loop held
// by its address or its setup (safe from any goroutine).
func (actor *Actor) wakeHeld() bool {
	switch actor.State() {
	case StateBusy, StatePaused, StateCapped, StateWorkstationOff, StateWorkstationDown, StateModelUnrecognized:
		return true
	}
	return actor.hasProblem(store.SymptomCloudAddress) || actor.hasProblem(store.SymptomCloudSetup)
}

func (actor *Actor) hasProblem(symptom string) bool {
	return slices.ContainsFunc(actor.Problems(), func(problem store.Problem) bool { return problem.Symptom == symptom })
}

// CheckWakes opens a wake_missed problem for each loop that has owed a
// turn for longer than the grace while free to take one. The scheduler
// calls it every 30 seconds, outside every actor, since an actor that has
// stopped answering can't report itself.
func (manager *Manager) CheckWakes() {
	ctx := context.Background()
	grace := HealthWakeGraceMinutes(ctx, manager.deps.Store.Settings(), manager.deps.Logger)
	if grace == 0 {
		return
	}
	manager.mu.RLock()
	names := make(map[string]string, len(manager.nameToID))
	for name, loopID := range manager.nameToID {
		names[loopID] = name
	}
	actors := maps.Clone(manager.byID)
	manager.mu.RUnlock()
	for loopID, actor := range actors {
		actor.checkMissedWake(loopID, names[loopID], time.Duration(grace)*time.Minute)
	}
}

// checkMissedWake opens the wake_missed problem from outside the actor, so
// it reads nothing the actor owns: the loop's ID and name come from the
// manager. A turn that starts, or a hold that comes, while the row is
// being stored withdraws it before anyone is told.
func (actor *Actor) checkMissedWake(loopID, loopName string, grace time.Duration) {
	actor.wakeMu.Lock()
	defer actor.wakeMu.Unlock()
	since := actor.wakeOwedSince()
	if !actor.wakeMissed(since) || now()-since < grace.Milliseconds() || actor.hasProblem(store.SymptomWakeMissed) {
		return
	}
	ctx := context.Background()
	log := actor.outsideLog(loopName)
	opened, err := actor.deps.Store.Problems().Open(ctx, loopID, store.SymptomWakeMissed, since)
	if err != nil {
		log.Error("open problem", "symptom", store.SymptomWakeMissed, "err", err)
		return
	}
	if !opened {
		return
	}
	if !actor.wakeMissed(actor.wakeOwedSince()) {
		if _, err := actor.deps.Store.Problems().Clear(ctx, loopID, store.SymptomWakeMissed, now(), store.ClearedByRecovery); err != nil {
			log.Error("clear problem", "symptom", store.SymptomWakeMissed, "err", err)
		}
		actor.loadProblemsOf(loopID)
		return
	}
	log.Warn("problem opened", "symptom", store.SymptomWakeMissed)
	actor.outsideEvent(loopID, "problem_opened", fmt.Sprintf(`{"symptom":%q,"since":%d}`, store.SymptomWakeMissed, since), log)
	actor.loadProblemsOf(loopID)
	actor.tellProblem(loopID, loopName, surface.ProblemNotice{Symptom: store.SymptomWakeMissed, Since: since, Opened: true})
}

// AcknowledgeMissedWake clears the loop's open wake_missed problem as the
// operator's doing, and reports whether there was one. It is done outside
// the actor, as the problem was opened, so an actor that has stopped
// answering doesn't hold the operator's request. A wake still owed is owed
// from now, so its grace starts over.
func (actor *Actor) AcknowledgeMissedWake(loopID, loopName string) bool {
	actor.wakeMu.Lock()
	defer actor.wakeMu.Unlock()
	log := actor.outsideLog(loopName)
	var since int64
	for _, problem := range actor.Problems() {
		if problem.Symptom == store.SymptomWakeMissed {
			since = problem.Since
		}
	}
	cleared, err := actor.deps.Store.Problems().Clear(context.Background(), loopID, store.SymptomWakeMissed, now(), store.ClearedByOperator)
	if err != nil {
		log.Error("clear problem", "symptom", store.SymptomWakeMissed, "err", err)
		return false
	}
	if !cleared {
		return false
	}
	actor.wakesMu.Lock()
	if actor.wakesAsked > 0 {
		actor.wakesAskedAt = now()
	}
	actor.wakesMu.Unlock()
	if owed := actor.owedSnap.Load(); owed != 0 {
		actor.owedSnap.CompareAndSwap(owed, now())
	}
	log.Info("problem cleared", "symptom", store.SymptomWakeMissed, "by", store.ClearedByOperator)
	actor.outsideEvent(loopID, "problem_cleared", fmt.Sprintf(`{"symptom":%q,"by":%q}`, store.SymptomWakeMissed, store.ClearedByOperator), log)
	actor.loadProblemsOf(loopID)
	actor.tellProblem(loopID, loopName, surface.ProblemNotice{Symptom: store.SymptomWakeMissed, Since: since, ByOperator: true})
	return true
}

// clearMissedWake clears the wake_missed problem as a turn starts, after
// turnStartedAt says the turn has.
func (actor *Actor) clearMissedWake() {
	actor.wakeMu.Lock()
	defer actor.wakeMu.Unlock()
	actor.clearProblem(store.SymptomWakeMissed, store.ClearedByRecovery)
}

// outsideLog is the actor's logger for code that runs outside the actor,
// which must not read the loop's name from the actor.
func (actor *Actor) outsideLog(loopName string) *slog.Logger {
	log := slog.Default()
	if actor.deps.Logger != nil {
		log = actor.deps.Logger
	}
	return log.With("loop", loopName)
}

// outsideEvent stores a spool event on the loop's timeline from outside
// the actor, with no session or turn, which only the actor knows.
func (actor *Actor) outsideEvent(loopID, subtype, payload string, log *slog.Logger) {
	event := &store.Event{LoopID: loopID, TS: now(), Type: "spool", Subtype: subtype, Payload: payload}
	if _, err := actor.deps.Store.Events().Insert(context.Background(), event); err != nil {
		log.Error("event insert", "err", err)
	}
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: loopID, Payload: event})
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
	actor.tellProblem(actor.loop.ID, actor.loop.Name, notice)
}

// tellProblem asks the control room to refetch the loop record, and hands
// the notice to the surfaces.
func (actor *Actor) tellProblem(loopID, loopName string, notice surface.ProblemNotice) {
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: loopID, Payload: map[string]any{
		"loop_id": loopID, "name": loopName, "problems_changed": true,
	}})
	notice.LoopID, notice.LoopName = loopID, loopName
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindLoopProblem, LoopID: loopID, Payload: &notice})
}

func (actor *Actor) loadProblems() { actor.loadProblemsOf(actor.loop.ID) }

// loadProblemsOf re-reads the open problems; the wake check opens one from
// outside the actor, so the reads take turns, and the last stored has seen
// every change made before it.
func (actor *Actor) loadProblemsOf(loopID string) {
	actor.problemsMu.Lock()
	defer actor.problemsMu.Unlock()
	open, err := actor.deps.Store.Problems().ListOpen(context.Background(), loopID)
	if err != nil {
		slog.Default().Error("read open problems", "loop_id", loopID, "err", err)
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
// wake goes without a turn, or a cloud loop's address fails its check or
// its setup isn't done, since the loop is not running at all, warning for
// errored turns or a long one, and ok with none.
func Health(problems []store.Problem) string {
	health := HealthOK
	for _, problem := range problems {
		if problem.Symptom == store.SymptomWorkstationDown || problem.Symptom == store.SymptomWakeMissed ||
			problem.Symptom == store.SymptomCloudAddress || problem.Symptom == store.SymptomCloudSetup {
			return HealthFailing
		}
		health = HealthWarning
	}
	return health
}
