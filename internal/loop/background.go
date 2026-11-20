package loop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// A loop's background commands (#1709). A command the loop starts in the
// background runs inside its claude process, and the CLI starts a turn of
// its own when one exits, with a task notification saying which. That
// holds only while the process lives, so an idle process with commands
// running is held open, for at most the hold, instead of closed after the
// idle timeout. Whatever ends the process first ends its commands, and the
// loop's next wake is told.

// sendTagged writes the turn's message under the turn's id, so the CLI's
// replay of it says which turn took it. A runtime whose process does not
// tag its messages (the cloud one) is sent the text alone.
func sendTagged(proc runtime.Proc, uuid, text string) error {
	if tagged, ok := proc.(interface{ SendTagged(uuid, text string) error }); ok {
		return tagged.SendTagged(uuid, text)
	}
	return proc.Send(text)
}

func (actor *Actor) backgroundHold() time.Duration {
	if actor.deps.BackgroundHold > 0 {
		return actor.deps.BackgroundHold
	}
	return backgroundHold
}

// mayHoldCommands reports whether an idle process may stay open for its
// background commands: only while the loop may take a turn, since a
// command's exit starts one the hub cannot stop.
func (actor *Actor) mayHoldCommands() bool {
	return !actor.paused && !actor.loop.WorkstationOff && !actor.held() && actor.loop.ModelRefusal == ""
}

// backgroundChanged follows the CLI's count of running background
// commands. An idle loop's timer is re-armed, since the count decides
// whether the process closes after the idle timeout or at the hold's end.
func (actor *Actor) backgroundChanged(count int) {
	if count == actor.background {
		return
	}
	actor.background = count
	actor.setBackground(context.Background())
	if actor.state == StateIdle && actor.turn == nil {
		actor.armIdleTimer()
	}
}

// endHeldCommands closes an idle process whose commands may no longer
// run, saying why. A busy one is judged when its turn ends.
func (actor *Actor) endHeldCommands(by string) {
	if actor.state != StateIdle || actor.background == 0 || actor.proc == nil {
		return
	}
	actor.endingBy = by
	actor.closeIdleProcess()
}

// stopHoldingWhy is why a loop that may not hold its commands is not
// allowed to: a pause or a power-off, or a cap.
func (actor *Actor) stopHoldingWhy() string {
	if actor.paused || actor.loop.WorkstationOff {
		return store.BackgroundEndedPause
	}
	if actor.held() {
		return store.BackgroundEndedCap
	}
	return store.BackgroundEndedExit
}

// commandsEnded runs as the process exits: the commands it still ran
// ended with it, and are owed to the loop's next wake.
func (actor *Actor) commandsEnded(killedFor string) {
	ended, by := actor.background, actor.endingBy
	actor.background, actor.endingBy = 0, ""
	actor.notified, actor.turnTaken, actor.replays = false, false, false
	if ended == 0 {
		return
	}
	if by == "" {
		switch {
		case killedFor == killedByOperator || strings.HasPrefix(killedFor, endedByVerb) || actor.paused || actor.loop.WorkstationOff:
			by = store.BackgroundEndedPause
		case actor.held():
			by = store.BackgroundEndedCap
		default:
			by = store.BackgroundEndedExit
		}
	}
	actor.oweEndedCommands(ended, by)
}

// oweEndedCommands adds ended commands to what the next wake is told, and
// the latest cause is the one it hears.
func (actor *Actor) oweEndedCommands(count int, by string) {
	actor.loop.BackgroundEnded += count
	actor.loop.BackgroundEndedBy = by
	actor.setBackground(context.Background())
	actor.storeSpoolEvent("background_ended", fmt.Sprintf(`{"count":%d,"by":%q}`, count, by))
}

// restoreBackground reads what the loop's row says at the actor's start:
// commands a process was running when the hub stopped ended with it.
func (actor *Actor) restoreBackground(ctx context.Context) {
	if actor.loop.BackgroundRunning == 0 {
		return
	}
	actor.loop.BackgroundEnded += actor.loop.BackgroundRunning
	actor.loop.BackgroundEndedBy = store.BackgroundEndedRestart
	actor.loop.BackgroundRunning = 0
	actor.setBackground(ctx)
}

// setBackground records the commands on the loop's row, so a restart
// knows which ended with the hub, and a told ending is not told again.
// Best effort, like the other runtime writes.
func (actor *Actor) setBackground(ctx context.Context) {
	actor.loop.BackgroundRunning = actor.background
	if err := actor.deps.Store.Loops().SetBackground(ctx, actor.loop.ID,
		actor.loop.BackgroundRunning, actor.loop.BackgroundEnded, actor.loop.BackgroundEndedBy); err != nil {
		actor.log().Error("persist background commands", "err", err)
	}
}

// takeEndedNote claims the note for the turn in flight: the note it opens
// with while ended commands are owed, "" when none are. The call marks the
// commands as carried, and endedNoteTold clears them when that turn ends.
func (actor *Actor) takeEndedNote() string {
	if actor.loop.BackgroundEnded == 0 {
		return ""
	}
	actor.endedTold = actor.loop.BackgroundEnded
	return BackgroundEndedNote(actor.loop.BackgroundEnded, actor.loop.BackgroundEndedBy, actor.backgroundHold())
}

// endedNoteTold clears what a finished turn told; endings since are kept.
func (actor *Actor) endedNoteTold() {
	if actor.endedTold == 0 {
		return
	}
	actor.loop.BackgroundEnded = max(0, actor.loop.BackgroundEnded-actor.endedTold)
	if actor.loop.BackgroundEnded == 0 {
		actor.loop.BackgroundEndedBy = ""
	}
	actor.endedTold = 0
	actor.setBackground(context.Background())
}

// openBackgroundTurn opens the turn the CLI starts itself on an idle
// process, which only a background command's exit does.
func (actor *Actor) openBackgroundTurn() {
	if actor.turn != nil || actor.state != StateIdle || actor.proc == nil {
		return
	}
	turn := &store.Turn{
		ID:        newUUID(),
		LoopID:    actor.loop.ID,
		SessionID: actor.loop.CurrentSessionID,
		Trigger:   store.TriggerBackground,
		StartedAt: now(),
	}
	if err := actor.deps.Store.Turns().Create(context.Background(), turn); err != nil {
		actor.log().Error("turn create", "err", err)
	}
	if actor.deps.OnTurnStart != nil {
		actor.deps.OnTurnStart(&actor.loop)
	}
	actor.turn = turn
	actor.turnTaken = true // no message of the hub's waits on it
	actor.lastCall = claude.Usage{}
	actor.watchTurn(turn)
	actor.turnStartedAt.Store(turn.StartedAt)
	actor.turnRefusal = ""
	actor.turnLimited, actor.turnLimitResets = false, 0
	actor.idleTimer.Stop()
	actor.state = StateBusy
	actor.publishState()
}

// finishBackgroundTurn records a turn the CLI ran for an exited command
// ahead of the hub's message, which it had not yet taken: the hub's turn
// goes on, and its result is still to come.
func (actor *Actor) finishBackgroundTurn(res *claude.ResultInfo) {
	if res == nil {
		return
	}
	turn := &store.Turn{
		ID:        newUUID(),
		LoopID:    actor.loop.ID,
		SessionID: actor.loop.CurrentSessionID,
		Trigger:   store.TriggerBackground,
		StartedAt: actor.turn.StartedAt,
	}
	if err := actor.deps.Store.Turns().Create(context.Background(), turn); err != nil {
		actor.log().Error("turn create", "err", err)
	}
	actor.recordTurnEnd(turn, res, false, false)
	actor.lastCall = claude.Usage{}
}
