package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
)

// Bounds are what a cloud turn's read-back is held to. What comes back is
// untrusted, and a turn over a bound ends as an error (ADR-0056).
type Bounds struct {
	// Poll is how often the events are read while a turn is in flight.
	Poll time.Duration
	// Status is how often the session's status is read while a turn waits
	// on its result: a session idle with no result written ends the turn.
	Status time.Duration
	// Quiet is how long a turn may go with no new event at all, the
	// backstop for a session whose status can't be read or never changes.
	// A single tool call can run quiet for long, so it is generous.
	Quiet time.Duration
	// Events and Bytes bound one turn's events, counted as passed on.
	Events int
	Bytes  int
}

// The read-back bounds a turn has by default.
var defaultBounds = Bounds{
	Poll:   time.Second,
	Status: 15 * time.Second,
	Quiet:  2 * time.Hour,
	Events: 20000,
	Bytes:  64 << 20,
}

func (bounds Bounds) withDefaults() Bounds {
	if bounds.Poll <= 0 {
		bounds.Poll = defaultBounds.Poll
	}
	if bounds.Status <= 0 {
		bounds.Status = defaultBounds.Status
	}
	if bounds.Quiet <= 0 {
		bounds.Quiet = defaultBounds.Quiet
	}
	if bounds.Events <= 0 {
		bounds.Events = defaultBounds.Events
	}
	if bounds.Bytes <= 0 {
		bounds.Bytes = defaultBounds.Bytes
	}
	return bounds
}

// pageSizes are the page sizes a poll asks for, each smaller one taken when
// the page before came back over maxResponse.
var pageSizes = []int{100, 10, 1}

// pollRetries is how many polls in a row may fail for now, a dropped
// connection or an answer of too many requests or of the API's own fault or
// overload, before the turn ends: one of them is not a changed API. They
// back off from the poll interval, doubling, to maxBackoff.
const pollRetries = 6

// maxBackoff caps the wait between failed polls, and a Retry-After past it.
const maxBackoff = 30 * time.Second

// backoff is the wait before the next poll after failures in a row: the
// poll interval doubled for each, or what the answer's Retry-After asked
// for when that is longer, never past maxBackoff.
func backoff(poll time.Duration, failures int, refusal *APIError) time.Duration {
	wait := poll << min(failures-1, 16)
	if refusal != nil && refusal.RetryAfter > wait {
		wait = refusal.RetryAfter
	}
	return min(wait, maxBackoff)
}

// sessionLost is the CLI's own words for a resume of a session it doesn't
// have, which the runner answers by starting a fresh one.
const sessionLost = "No conversation found with session ID: "

// proc is a loop's cloud session, seen by the runner as the claude process
// it would be in a workstation: Send starts a turn, and Events yields the
// turn's stream-json events, read back from the session's event stream.
type proc struct {
	cloud         *Runtime
	loopID        string
	title         string
	model         string
	prompt        string
	repo          string
	hubToken      string
	environmentID string
	bounds        Bounds

	// claudeSession is the hub's session id, which the events carry in
	// place of the cloud claude's own. sessionID is the cloud session, ""
	// until the first Send creates one; retired is the one a fresh session
	// replaces, archived once its successor exists.
	claudeSession string
	sessionID     string
	retired       string
	afterID       string
	keptID        string // the afterID the state file holds
	// lost says the hub resumed a session this runtime holds no cloud
	// session for.
	lost bool

	sends  chan string
	events chan claude.Event
	ctx    context.Context
	stop   context.CancelFunc

	mu      sync.Mutex
	stopped bool // CloseStdin or Kill was called
	killed  bool
	exited  chan struct{}
	exit    claude.ExitInfo
}

func (session *proc) start() {
	session.sends = make(chan string, 1)
	session.events = make(chan claude.Event, 64)
	session.exited = make(chan struct{})
	session.ctx, session.stop = context.WithCancel(context.Background())
	go session.run()
}

// Send starts a turn. The message is sent from the session's own
// goroutine, so the runner doesn't wait on the API; a send that fails ends
// the turn as an error.
func (session *proc) Send(text string) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.stopped {
		return errors.New("cloud: the session is closed")
	}
	select {
	case session.sends <- text:
		return nil
	default:
		return errors.New("cloud: a turn is already in flight")
	}
}

func (session *proc) Events() <-chan claude.Event { return session.events }

// CloseStdin ends the Proc between turns: the cloud session itself goes on,
// idle, for the next wake to send into.
func (session *proc) CloseStdin() error {
	session.close(false)
	return nil
}

// Kill interrupts the turn in flight, if any, and ends the Proc.
func (session *proc) Kill() error {
	session.close(true)
	return nil
}

func (session *proc) close(kill bool) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.stopped {
		return
	}
	session.stopped, session.killed = true, kill
	session.stop()
}

func (session *proc) Wait() claude.ExitInfo {
	<-session.exited
	return session.exit
}

// PID is 0: there is no process of the hub's to reap.
func (session *proc) PID() int { return 0 }

func (session *proc) run() {
	defer close(session.exited)
	defer close(session.events)
	if session.lost {
		// as the CLI ends a resume it can't load: before any event
		session.exit = claude.ExitInfo{Code: 1, Stderr: sessionLost + session.claudeSession}
		return
	}
	for {
		select {
		case <-session.ctx.Done():
			session.finish()
			return
		case text := <-session.sends:
			if lost := session.turn(text); lost {
				session.exit = claude.ExitInfo{Code: 1, Stderr: sessionLost + session.claudeSession}
				return
			}
		}
	}
}

// finish is the Proc's end after CloseStdin or Kill. A kill interrupts the
// session's turn, so it stops spending; the interrupt is sent on a context
// of its own, since the Proc's is gone.
func (session *proc) finish() {
	session.mu.Lock()
	killed := session.killed
	session.mu.Unlock()
	if !killed {
		return
	}
	session.exit = claude.ExitInfo{Code: -1, Stderr: "killed"}
	if session.sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.cloud.Client.Interrupt(ctx, session.sessionID); err != nil && !isInactive(err) {
		session.exit.Stderr = "killed; the interrupt failed: " + err.Error()
	}
}

// turn sends one message and passes the turn's events on until its result.
// It reports whether the session is gone, archived on Anthropic's side.
func (session *proc) turn(text string) (lost bool) {
	messageID := newUUID()
	if session.sessionID == "" {
		if err := session.create(text, messageID); err != nil {
			session.fail(err.Error())
			return false
		}
	} else if err := session.cloud.Client.SendMessage(session.ctx, session.sessionID, text, messageID); err != nil {
		if isInactive(err) {
			return true
		}
		session.fail(err.Error())
		return false
	}
	session.read(messageID)
	return false
}

// create starts the cloud session with its first message, and archives the
// one it replaces. A session the retired one is still running a turn in
// would go on spending, and nothing reads it any more.
func (session *proc) create(text, messageID string) error {
	created, err := session.cloud.Client.CreateSession(session.ctx, SessionSpec{
		EnvironmentID:      session.environmentID,
		Title:              session.title,
		Model:              session.model,
		Repo:               session.repo,
		AppendSystemPrompt: session.prompt,
		Env:                map[string]string{HubTokenVar: session.hubToken},
		FirstMessage:       text,
		MessageID:          messageID,
	})
	if err != nil {
		return err
	}
	session.sessionID, session.afterID = created.ID, ""
	if err := session.cloud.update(session.loopID, func(s *state) {
		s.SessionID, s.ClaudeSession, s.AfterID = created.ID, session.claudeSession, ""
		s.SessionURL = created.URL
		s.SessionToken = tokenHash(session.hubToken)
	}); err != nil {
		return fmt.Errorf("cloud: keeping the session's id: %w", err)
	}
	if session.retired != "" && session.retired != created.ID {
		// Best effort: the new session is what counts, and a retired one
		// left unarchived only idles.
		_ = session.cloud.Client.ArchiveSession(session.ctx, session.retired)
		session.retired = ""
	}
	return nil
}

// noise is what the session's event stream carries that its claude did not
// emit, or that the runner has no use for: the environment manager's log,
// the control protocol, and the CLI's own bookkeeping.
var noise = map[string]bool{
	"env_manager_log":        true,
	"control_request":        true,
	"control_response":       true,
	"control_cancel_request": true,
	"active_goal":            true,
	"autocompact_state":      true,
}

// read passes on the turn's events, from the echo of the message that
// started it to its result. Anything before the echo belongs to an earlier
// turn, one a kill stopped reading, and is skipped. It ends the turn as an
// error, naming what it saw, on an unreadable answer or event, on a
// refusal for good or a run of refusals for now, on a bound, on a session
// that parks or idles with no result or with the message never echoed, and
// on the quiet backstop.
func (session *proc) read(messageID string) {
	var (
		echoed      bool
		count, size int
		lastEvent   = time.Now() // the send, until the echo
		nextStatus  = time.Now().Add(session.bounds.Status)
		idleOnce    bool
		failures    int
		pageSize    int // index into pageSizes
	)
	for {
		page, err := session.cloud.Client.Events(session.ctx, session.sessionID, session.afterID, pageSizes[pageSize])
		if session.ctx.Err() != nil {
			return
		}
		var apiErr *APIError
		switch {
		case errors.As(err, &apiErr) && apiErr.Oversized && pageSize < len(pageSizes)-1:
			pageSize++
			continue
		case errors.As(err, &apiErr) && apiErr.Oversized:
			// one event over the bound: the page's tail names it, which is
			// how the next poll gets past it
			if id := apiErr.LastID(); id != "" {
				session.keepCursor(id)
				session.fail(fmt.Sprintf("%s: one event is over %d bytes and was skipped", apiErr.Endpoint, maxResponse))
				return
			}
			session.fail(fmt.Sprintf("%s; past %d bytes it can't be read past, so every turn of this session will fail the same way until the loop's session is rotated",
				err, maxSkipped))
			return
		case apiErr != nil && !apiErr.Transient():
			session.fail(err.Error())
			return
		case err != nil:
			// a dropped connection, too many requests, or the API's own
			// fault or overload: for now, not for good
			if failures++; failures >= pollRetries {
				session.fail(fmt.Sprintf("%v (%d polls in a row)", err, failures))
				return
			}
			if !session.sleep(backoff(session.bounds.Poll, failures, apiErr)) {
				return
			}
			continue
		}
		failures, pageSize = 0, 0
		for _, raw := range page.Events {
			var probe struct {
				Type      string `json:"type"`
				UUID      string `json:"uuid"`
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(raw, &probe); err != nil || probe.Type == "" || probe.UUID == "" {
				session.keepCursor(page.LastID)
				session.fail(fmt.Sprintf("GET /v1/sessions/{id}/events: an event it can't read: %s", snippet(raw)))
				return
			}
			session.afterID = probe.UUID
			if !echoed {
				// what comes before is an earlier turn's, and doesn't hold
				// the quiet backstop off
				if echoed = probe.Type == "user" && probe.UUID == messageID; echoed {
					lastEvent = time.Now()
				}
				continue
			}
			lastEvent = time.Now()
			// Events the session's claude emits carry its session id; what
			// the API and the environment add carries none.
			if noise[probe.Type] || probe.SessionID == "" {
				continue
			}
			if count, size = count+1, size+len(raw); count > session.bounds.Events || size > session.bounds.Bytes {
				session.keepCursor(session.afterID)
				session.fail(fmt.Sprintf("the turn ran past its bound of %d events or %d bytes", session.bounds.Events, session.bounds.Bytes))
				return
			}
			event := claude.DecodeEvent(session.asHubSession(raw))
			if !session.pass(event) {
				return
			}
			if event.Type == "result" {
				session.keepCursor(session.afterID)
				return
			}
		}
		if page.LastID != "" {
			session.afterID = page.LastID
		}
		session.keepCursor(session.afterID)
		if page.HasMore {
			continue
		}
		if time.Since(lastEvent) > session.bounds.Quiet {
			session.fail(fmt.Sprintf("the session wrote no event for %s", session.bounds.Quiet))
			return
		}
		if !time.Now().Before(nextStatus) {
			nextStatus = time.Now().Add(session.bounds.Status)
			status, err := session.cloud.Client.GetSession(session.ctx, session.sessionID)
			switch {
			case session.ctx.Err() != nil:
				return
			case err != nil:
				// the quiet backstop is for a status that can't be read
			case status.Status == StatusRequiresAction:
				session.fail("the session is waiting for an answer the hub can't give (session_status requires_action)")
				return
			case status.Status == StatusIdle && idleOnce && !echoed:
				session.fail("the message never reached the session: it is idle and never echoed it (session_status idle)")
				return
			case status.Status == StatusIdle && idleOnce:
				session.fail("the session went idle with no result written (session_status idle)")
				return
			default:
				// idle once may be a message not yet taken, or a result
				// still being written: one more round of polls decides
				idleOnce = status.Status == StatusIdle
			}
		}
		if !session.sleep(session.bounds.Poll) {
			return
		}
	}
}

// asHubSession rewrites an event's session id to the hub's: the runner
// takes the id an init carries as the session's own, and resumes by it.
func (session *proc) asHubSession(raw json.RawMessage) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw
	}
	id, _ := json.Marshal(session.claudeSession)
	fields["session_id"] = id
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return rewritten
}

// keepCursor records how far the session's events have been read, so the
// next wake reads on from there.
func (session *proc) keepCursor(afterID string) {
	if afterID == "" || afterID == session.keptID {
		return
	}
	session.afterID, session.keptID = afterID, afterID
	_ = session.cloud.update(session.loopID, func(s *state) {
		if s.SessionID == session.sessionID {
			s.AfterID = afterID
		}
	})
}

// fail ends the turn as an error that says what went wrong, as a result
// event the runner reads like any other.
func (session *proc) fail(reason string) {
	raw, _ := json.Marshal(map[string]any{
		"type":       "result",
		"subtype":    "error_during_execution",
		"is_error":   true,
		"result":     "cloud runtime: " + reason,
		"session_id": session.claudeSession,
	})
	session.pass(claude.DecodeEvent(raw))
}

// pass hands an event to the runner, unless the Proc is ending.
func (session *proc) pass(event claude.Event) bool {
	select {
	case session.events <- event:
		return true
	case <-session.ctx.Done():
		return false
	}
}

func (session *proc) sleep(wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-session.ctx.Done():
		return false
	}
}
