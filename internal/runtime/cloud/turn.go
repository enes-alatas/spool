package cloud

import (
	"log/slog"
	"strings"
	"time"
)

// A turn the hub sent into a loop's session counts as running from the
// send until its Stop post (ADR-0059). The CLI can't interrupt a session,
// so a stop, a kill or a rotation leaves the turn running in the VM, and
// with the loop's token revoked its Stop post never reaches the hub. Once
// cut off, a turn counts as running until its quiet bound, the backstop
// for a post that never comes. The loop's page says so, and links the
// turn's session, where the operator can interrupt it.

// turnState is the turn the state file records: the one in flight, or a
// cut-off one whose quiet bound hasn't passed. Times are Unix milliseconds.
type turnState struct {
	Since      int64  `json:"since"`
	Until      int64  `json:"until"`
	SessionURL string `json:"session_url"`
	CutOff     bool   `json:"cut_off,omitempty"`
}

// Turn is a loop's turn that may be running in its session, as the loop's
// page shows it. Since is the hub's send, which a turn about to run shares
// with one running; Until is its quiet bound. SessionURL is the turn's
// session, which after a rotation is not the loop's current one. CutOff
// says the hub no longer hears the turn end, so only Until clears it.
type Turn struct {
	Since      int64
	Until      int64
	SessionURL string
	CutOff     bool
}

// Turn is the loop's turn that may be running, false when there is none:
// none was sent, its Stop post came, or its quiet bound has passed.
func (cloud *Runtime) Turn(loopID string) (Turn, bool) {
	current, err := cloud.load(loopID)
	if err != nil || current.Turn == nil || time.Now().UnixMilli() >= current.Turn.Until ||
		!strings.HasPrefix(current.Turn.SessionURL, sessionPages) {
		return Turn{}, false
	}
	return Turn(*current.Turn), true
}

// beginTurn records a turn sent at since into the loop's session, bounded
// by quiet, and returns it. A cut-off turn whose bound hasn't passed is
// kept instead: a turn the hub can still hear end shows on the loop's
// state, and one it can't is the one the page must name.
func (cloud *Runtime) beginTurn(loopID string, since time.Time, quiet time.Duration) turnState {
	sent := turnState{Since: since.UnixMilli(), Until: since.Add(quiet).UnixMilli()}
	cloud.changeTurn(loopID, func(current *state) {
		sent.SessionURL = current.SessionURL
		if kept := current.Turn; kept != nil && kept.CutOff && sent.Since < kept.Until {
			return
		}
		current.Turn = &sent
	})
	return sent
}

// endTurn forgets the turn sent at since, once its Stop post came or its
// bound passed. A cut-off turn is left to its bound.
func (cloud *Runtime) endTurn(loopID string, since int64) {
	cloud.changeTurn(loopID, func(current *state) {
		if current.Turn != nil && current.Turn.Since == since && !current.Turn.CutOff {
			current.Turn = nil
		}
	})
}

// cutOffTurn marks sent as a turn the hub no longer hears end: its session
// was closed while it ran. One sent while an earlier cut-off turn was kept
// takes that one's place, since its bound is the later.
func (cloud *Runtime) cutOffTurn(loopID string, sent turnState) {
	cloud.changeTurn(loopID, func(current *state) {
		sent.CutOff = true
		current.Turn = &sent
	})
}

func (cloud *Runtime) changeTurn(loopID string, change func(*state)) {
	if err := cloud.update(loopID, change); err != nil {
		slog.Default().Warn("cloud: the loop's turn wasn't recorded", "loop", loopID, "err", err)
	}
}
