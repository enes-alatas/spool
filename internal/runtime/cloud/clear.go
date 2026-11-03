package cloud

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// A rotation by choice ends the loop's hub session and starts another
// (ADR-0022). On cloud, the next hub session carries on in the cloud
// session the last one ran in, its context emptied with /clear, so a
// rotation leaves no session behind on claude.ai (#1278). The /clear makes
// the session post SessionStart with source clear, which the hub answers
// as it answers a start: with the loop's connections and its prompt, so
// a cleared context holds the mission and a new shell's variables, as a
// new session would.

// clearCommand is what clears a session's context, sent as a turn's text.
const clearCommand = "/clear"

// pendingClear is a /clear the hub sent into a loop's session, waiting for
// the session's SessionStart post: answer is what that post is answered
// with, and done is closed once it is.
type pendingClear struct {
	sessionID string
	answer    []byte
	done      chan struct{}
	answered  bool
}

func (waits *hookWaits) awaitClear(loopID, sessionID string, answer []byte) *pendingClear {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.clears == nil {
		waits.clears = map[string]*pendingClear{}
	}
	pending := &pendingClear{sessionID: sessionID, answer: answer, done: make(chan struct{})}
	waits.clears[loopID] = pending
	return pending
}

func (waits *hookWaits) forgetClear(loopID string, pending *pendingClear) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.clears[loopID] == pending {
		delete(waits.clears, loopID)
	}
}

// answerClear answers a /clear's SessionStart post from the loop's session
// while the hub waits on a /clear it sent there, once: any other is
// answered as a compaction's is (answerContextLost).
func (cloud *Runtime) answerClear(loopID, sessionID string) ([]byte, bool) {
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	pending := cloud.waits.clears[loopID]
	if pending == nil || pending.answered || pending.sessionID != sessionID {
		return nil, false
	}
	pending.answered = true
	close(pending.done)
	return pending.answer, true
}

// clear empties the context of the loop's cloud session with /clear, and
// waits, within the start's bound, for the session's SessionStart post
// that says it was emptied. It then records the session as the hub
// session's. It runs before the turn's Stop is awaited: /clear runs no
// model turn, and a Stop post it made would be no turn's.
func (session *cliSession) clear() error {
	answer, err := sessionStartAnswer(exportLines(session.env), session.prompt)
	if err != nil {
		return err
	}
	pending := session.cloud.waits.awaitClear(session.loopID, session.sessionID, answer)
	defer session.cloud.waits.forgetClear(session.loopID, pending)
	if err := session.send(clearCommand); err != nil {
		return err
	}
	within := session.cloud.CLI.StartWithin
	if within <= 0 {
		within = defaultStartWithin
	}
	bound := time.NewTimer(within)
	defer bound.Stop()
	select {
	case <-pending.done:
	case <-bound.C:
		return fmt.Errorf("cloud: the session posted nothing for its /clear within %s", within)
	case <-session.ctx.Done():
		return session.ctx.Err()
	}
	return session.cloud.update(session.loopID, func(current *state) {
		current.ClaudeSession = session.claudeSession
	})
}

// clearOrStartAnew clears the session for a rotation's first turn. A
// clear that fails leaves the turn to start a session of its own, as a
// rotation did before #1278: the old one may hold the last context still.
func (session *cliSession) clearOrStartAnew() {
	session.clearFirst = false
	err := session.clear()
	if err == nil {
		return
	}
	if errors.Is(err, session.ctx.Err()) && session.ctx.Err() != nil {
		return
	}
	slog.Default().Warn("cloud: a rotation couldn't clear its session, so it starts one of its own", "loop", session.loopID, "session", session.sessionID, "err", err)
	session.sessionID, session.account = "", ""
}
