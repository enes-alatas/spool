package loop

import (
	"context"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// usageLimitFurthest is the furthest off a refusal's reset is taken: the
// plan's longest window, seven days, with resetSlack for a hub clock behind
// the API's, as planWindowFrom bounds a window's. A reset further off, one
// in the wrong unit or read on a clock far behind, would hold the loop past
// any real reset, so it counts as no reset named.
const usageLimitFurthest = 7*24*time.Hour + resetSlack

// usageLimitResetKnown reports whether a refusal at the time at named a
// reset the hold can wait for: one ahead, and no further off than
// usageLimitFurthest.
func usageLimitResetKnown(resets, at int64) bool {
	return resets > at && resets <= at+usageLimitFurthest.Milliseconds()
}

// holdForUsageLimit holds the loop on a turn the API refused on the plan's
// usage limit (#1649). Every turn would be refused the same way until the
// limit resets, and each refused turn stays in the session: the CLI writes
// a turn's prompt to the session file before it calls the API. So a loop
// that kept taking turns woke from the outage to every tick and note of it
// at once, each note as it stood when its turn was refused (#1650).
//
// The loop is held as a capped one is: its ticks are dropped and its
// messages kept for the lift (capChanged). A refusal that named a reset
// holds it until then. One that named none, or none the hold can trust,
// holds it until the operator continues it, by acknowledging the
// usage_limit problem it opens and tells the owner of, since only they
// can tell when the limit has reset; a guessed retry would only be refused
// again. The refused batch goes back to the front of the queue with its
// latest tick alone, as a refused login's does (#420). The
// standing-instructions note it carried is already in the session, so it
// counts as told: the turn after the lift gets a note only when the
// instructions changed again meanwhile, rendered as they are then.
func (actor *Actor) holdForUsageLimit(resets int64) {
	at := now()
	actor.wasCapped = true
	if actor.usageLimitTimer != nil {
		actor.usageLimitTimer.Stop()
	}
	if usageLimitResetKnown(resets, at) {
		actor.usageLimitUntil, actor.usageLimitOpen = resets, false
		if actor.usageLimitTimer != nil {
			actor.usageLimitTimer.Reset(time.Duration(resets-at) * time.Millisecond)
		}
		actor.log().Warn("the plan's usage limit refused a turn; holding the loop until it resets",
			"until", time.UnixMilli(resets).UTC().Format(time.RFC3339))
	} else {
		actor.usageLimitUntil, actor.usageLimitOpen = 0, true
		actor.log().Warn("the plan's usage limit refused a turn and named no reset; holding the loop until the operator continues it",
			"resets", resets)
		actor.openProblem(surface.ProblemNotice{Symptom: store.SymptomUsageLimit, Since: at})
	}
	if actor.promptDelta != "" {
		actor.promptDelta = ""
		actor.setPromptHash(context.Background(), actor.promptHash)
	}
	actor.inbox = latestTickOnly(append(append([]Envelope(nil), actor.currentBatch...), actor.inbox...))
	actor.drainRefusedProcess()
}

// usageLimited reports whether a usage limit that refused a turn holds the
// loop: until its reset, or until the operator continues it.
func (actor *Actor) usageLimited() bool {
	return actor.usageLimitOpen || actor.usageLimitUntil > now()
}

// usageLimitReset lets a loop the usage limit held go when the limit
// resets, as a cap's lift does. A timer that fires early, on a clock set
// back, is re-armed for the rest.
func (actor *Actor) usageLimitReset() {
	if rest := actor.usageLimitUntil - now(); rest > 0 {
		actor.usageLimitTimer.Reset(time.Duration(rest) * time.Millisecond)
		return
	}
	actor.usageLimitUntil = 0
	actor.capChanged()
}

// usageLimitContinued lets a loop go that a usage limit with no known reset
// held, once the operator acknowledged its usage_limit problem: they say
// the limit has reset. If it hasn't, the next turn is refused and holds the
// loop again.
func (actor *Actor) usageLimitContinued() {
	if !actor.usageLimitOpen {
		return
	}
	actor.usageLimitOpen = false
	actor.log().Info("the operator continued a loop the usage limit held")
	actor.capChanged()
}

// UsageLimitUntil returns when the usage limit holding the loop resets, as
// last published: 0 while none holds it, or while one holds it until the
// operator continues it (safe from any goroutine).
func (actor *Actor) UsageLimitUntil() int64 { return actor.limitSnap.Load() }

// loginMoved lifts the usage limit's hold when an edit moved the loop off
// the Claude login named before: the limit is the login's, so the loop is
// no longer under it, as a loop moved off a capped login's plan cap is not.
// A new login whose limit is spent too refuses the next turn, which holds
// the loop again. A usage_limit problem open stays open until a turn runs.
func (actor *Actor) loginMoved(before string) {
	if !actor.usageLimited() || sameLogin(before, actor.login()) {
		return
	}
	actor.usageLimitUntil, actor.usageLimitOpen = 0, false
	actor.usageLimitTimer.Stop()
	actor.log().Info("the loop moved to another Claude login; the usage limit no longer holds it")
}

// usageLimitPassed clears the usage_limit problem once a turn has run
// without the limit refusing it.
func (actor *Actor) usageLimitPassed() {
	if actor.hasProblem(store.SymptomUsageLimit) {
		actor.clearProblem(store.SymptomUsageLimit, store.ClearedByRecovery)
	}
}

// sameLogin reports whether two logins, as loops name them, are one.
func sameLogin(left, right string) bool {
	return left == right || isDefaultLogin(left) && isDefaultLogin(right)
}
