package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// CLI is the claude CLI cloud sessions run through, and where its runs
// are made (ADR-0059): `claude --cloud` under a pseudo-terminal starts a
// session, `claude -p --cloud` sends into it, and each turn is read back
// from what the session's Stop hook posts.
type CLI struct {
	// Binary is the claude CLI.
	Binary string
	// Dir is where each run's start directory and config dir are made,
	// outside the data directory, and removed once the run ends.
	Dir string
	// Env is the CLI's environment: the hub's, with no credential of its
	// own. Each run adds its config dir and the account's token.
	Env []string
	// StartWithin bounds a start from running the CLI to the session's
	// SessionStart post; two minutes when zero.
	StartWithin time.Duration
}

// Bounds are what a cloud turn's read-back is held to. What the Stop hook
// posts is untrusted, and a turn over a bound ends as an error (ADR-0059).
type Bounds struct {
	// Quiet is how long a turn may go with no Stop post, the backstop for
	// a post that never comes: the session's status can't be read without
	// the sessions API. A single tool call can run quiet for long, so it is
	// generous.
	Quiet time.Duration
	// Events and Bytes bound one turn's events, counted as passed on.
	Events int
	Bytes  int
}

// The read-back bounds a turn has by default.
var defaultBounds = Bounds{
	Quiet:  2 * time.Hour,
	Events: 20000,
	Bytes:  64 << 20,
}

func (bounds Bounds) withDefaults() Bounds {
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

// sessionLost is the CLI's own words for a resume of a session it doesn't
// have, which the runner answers by starting a fresh one.
const sessionLost = "No conversation found with session ID: "

// The bound on a start, and on a send's CLI run.
const (
	defaultStartWithin = 2 * time.Minute
	sendWithin         = 2 * time.Minute
)

// hookWaits are the hook posts the runtime is waiting on: a start's
// SessionStart and a turn's Stop, each by loop, and the prompt each loop's
// session runs with, which a compaction's post is answered with.
type hookWaits struct {
	mu      sync.Mutex
	starts  map[string]*pendingStart
	stops   map[string]chan stopArrival
	prompts map[string]string
	// ended is when each loop's last turn ended: /mcp takes its session's
	// calls for turnGrace after.
	ended map[string]time.Time
	// binding is the session a SessionStart post just bound, until the
	// start has recorded it in the loop's state: the session's first MCP
	// call can come in between.
	binding map[string]string
	// checks are the setup checks waiting on their session, by loop, and
	// checked the session of each loop's last check (check.go).
	checks  map[string]*pendingCheck
	checked map[string]string
}

// setPrompt records the prompt the loop's session runs with.
func (waits *hookWaits) setPrompt(loopID, prompt string) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.prompts == nil {
		waits.prompts = map[string]string{}
	}
	waits.prompts[loopID] = prompt
}

// pendingStart is a start of a loop waiting for its session's id: it takes
// one post, before its deadline, that names the session its CLI printed.
// It answers that post with answer, the loop's connections and prompt.
type pendingStart struct {
	deadline time.Time
	session  chan string
	answer   []byte
	// heard is whether any SessionStart post of the loop came while the
	// start waited, a refused one too: its session reached the hub.
	heard bool
	// viewed is the session the CLI printed it had created, "" until the
	// hub reads it; seen is closed then.
	viewed string
	seen   chan struct{}
}

// stopArrival is a Stop post as the turn in flight takes it.
type stopArrival struct {
	post      StopPost
	overBound bool
}

func (waits *hookWaits) awaitStart(loopID string, within time.Duration, answer []byte) *pendingStart {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.starts == nil {
		waits.starts = map[string]*pendingStart{}
	}
	start := &pendingStart{deadline: time.Now().Add(within), session: make(chan string, 1), answer: answer, seen: make(chan struct{})}
	waits.starts[loopID] = start
	return start
}

func (waits *hookWaits) forgetStart(loopID string, start *pendingStart) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.starts[loopID] == start {
		delete(waits.starts, loopID)
	}
}

// SessionPosted is told of a SessionStart post of the loop that names a
// session, before the hub reads it any further: a post the hub refuses,
// for an id of a shape it doesn't know or a body it can't read, still
// came from the session, so it reached the hub (#1093).
func (cloud *Runtime) SessionPosted(loopID string) {
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	if start := cloud.waits.starts[loopID]; start != nil {
		start.heard = true
	}
}

// viewed records the session start's CLI printed it had created. Only the
// first counts: the CLI prints one.
func (waits *hookWaits) viewed(start *pendingStart, sessionID string) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if start.viewed == "" {
		start.viewed = sessionID
		close(start.seen)
	}
}

// heard reports whether a SessionStart post of start's loop came while it
// waited.
func (waits *hookWaits) heard(start *pendingStart) bool {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	return start.heard
}

func (waits *hookWaits) awaitStop(loopID string) chan stopArrival {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.stops == nil {
		waits.stops = map[string]chan stopArrival{}
	}
	stops := make(chan stopArrival, 1)
	waits.stops[loopID] = stops
	return stops
}

func (waits *hookWaits) forgetStop(loopID string, stops chan stopArrival) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.stops[loopID] == stops {
		delete(waits.stops, loopID)
	}
	waits.endedLocked(loopID)
}

// endedLocked records that the loop's turn ended now. waits.mu is held.
func (waits *hookWaits) endedLocked(loopID string) {
	if waits.ended == nil {
		waits.ended = map[string]time.Time{}
	}
	waits.ended[loopID] = time.Now()
}

// SessionStart takes a SessionStart post and answers it with the loop's
// connections as export lines (ADR-0059) and the hub's prompt
// (sessionStartAnswer). A startup post's session id is recorded as the
// loop's while a start of it is pending, within its bound, once, and only
// if it names the session the start's CLI printed it had created
// (ADR-0061): every session in the loop's environment reaches the hub on
// its token. A compaction's or a /clear's post is answered with the prompt
// alone, and only from the loop's recorded session while a turn of it is
// in flight. Any other post is refused: anything in the VM can post with
// the token, and must not rebind the loop's wakes into another session or
// read the connections at will. A startup answer is told to HandOff, and
// so is a refused startup post from the loop's own session, which may have
// lost them. Each answer is logged, without its values.
func (cloud *Runtime) SessionStart(ctx context.Context, loopID, sessionID string, hook []byte) ([]byte, error) {
	sessionID = sessionAddress(sessionID)
	source := hookSource(hook)
	if source == sourceStartup {
		if answer, ok := cloud.answerCheck(ctx, loopID, sessionID); ok {
			slog.Default().Info("cloud: a setup check's session reached the hub", "loop", loopID, "session", sessionID)
			return answer, nil
		}
	}
	if source == sourceCompact || source == sourceClear {
		answer, err := cloud.answerContextLost(loopID, sessionID)
		if err == nil {
			slog.Default().Info("cloud: a session was handed its loop's prompt again", "loop", loopID, "source", source, "session", sessionID)
		}
		return answer, err
	}
	answer, err := cloud.answerSessionStart(ctx, loopID, sessionID, source)
	if err == nil {
		slog.Default().Info("cloud: a session was handed its loop's connections", "loop", loopID, "source", source, "session", sessionID)
	}
	if cloud.HandOff != nil {
		if err == nil {
			cloud.HandOff(loopID, true)
		} else if current, loadErr := cloud.load(loopID); loadErr == nil && current.SessionID == sessionID {
			cloud.HandOff(loopID, false)
		}
	}
	return answer, err
}

// answerContextLost answers a compaction's or a /clear's SessionStart post
// with the prompt the loop's session was started with, when it comes from
// that session while a turn of it is in flight.
func (cloud *Runtime) answerContextLost(loopID, sessionID string) ([]byte, error) {
	current, err := cloud.load(loopID)
	if err != nil {
		return nil, err
	}
	if current.SessionID == "" || current.SessionID != sessionID {
		return nil, errNotTheSession
	}
	cloud.waits.mu.Lock()
	inFlight := cloud.waits.stops[loopID] != nil
	prompt, known := cloud.waits.prompts[loopID]
	cloud.waits.mu.Unlock()
	if !inFlight || !known {
		return nil, errNoTurn
	}
	return sessionStartAnswer(nil, prompt)
}

// sessionAddress is a session's id as claude.ai's pages and `-p --cloud`
// name it, session_…. The VM's CLAUDE_CODE_REMOTE_SESSION_ID names the
// same session cse_… (run A on #957), and a start recorded as that one
// was never sent into again (#1099).
func sessionAddress(sessionID string) string {
	if rest, ok := strings.CutPrefix(sessionID, "cse_"); ok {
		return "session_" + rest
	}
	return sessionID
}

func (cloud *Runtime) answerSessionStart(ctx context.Context, loopID, sessionID, source string) ([]byte, error) {
	cloud.waits.mu.Lock()
	start := cloud.waits.starts[loopID]
	if start != nil {
		start.heard = true
	}
	cloud.waits.mu.Unlock()
	if source != sourceStartup {
		return nil, fmt.Errorf("cloud: a SessionStart post from %q is never answered", source)
	}
	if start == nil || time.Now().After(start.deadline) {
		return nil, errNoHandOff
	}
	// The VM can post before the hub has read the CLI's line through the
	// terminal, and the hook doesn't post again, so the post waits for the
	// line, within the start's bound, and is judged then.
	deadline := time.NewTimer(time.Until(start.deadline))
	defer deadline.Stop()
	select {
	case <-start.seen:
	case <-deadline.C:
		return nil, errNoHandOff
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	if cloud.waits.starts[loopID] != start {
		return nil, errNoHandOff
	}
	if sessionID != start.viewed {
		return nil, errOtherSession
	}
	delete(cloud.waits.starts, loopID)
	if cloud.waits.binding == nil {
		cloud.waits.binding = map[string]string{}
	}
	cloud.waits.binding[loopID] = sessionID
	start.session <- sessionID
	return start.answer, nil
}

// Stop takes a Stop post from the loop's recorded session: it ends the
// loop's turn in flight, once. A post from any other session of the
// loop's environment, or with no turn in flight, is refused (ADR-0061).
func (cloud *Runtime) Stop(_ context.Context, loopID, sessionID string, hook, lines []byte, overBound bool) error {
	if cloud.waits.checkSession(loopID, sessionID) {
		return nil // a setup check's turn, which nothing waits on
	}
	current, err := cloud.recorded(loopID)
	if err != nil {
		return err
	}
	if current.SessionID == "" || sessionAddress(sessionID) != current.SessionID {
		return errNotTheSession
	}
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	stops := cloud.waits.stops[loopID]
	if stops == nil {
		return errNoTurn
	}
	delete(cloud.waits.stops, loopID)
	cloud.waits.endedLocked(loopID)
	stops <- stopArrival{post: StopPost{Hook: hook, Lines: lines}, overBound: overBound}
	return nil
}

// turnGrace is how long after a turn ends /mcp still takes its session's
// calls (ADR-0061).
const turnGrace = time.Minute

// MCPSession says whether sessionID is the loop's own session, at any
// time: its MCP client may connect and list the hub's tools between turns
// too, since a client refused there stays failed into the next turn
// (ADR-0061). While a start is pending it is the session the start's CLI
// printed: the client connects before SessionStart binds it, and a
// request that comes before the hub has read the CLI's line waits for it
// within the start's bound, as an early SessionStart post does.
func (cloud *Runtime) MCPSession(ctx context.Context, loopID, sessionID string) error {
	_, err := cloud.boundSession(ctx, loopID, sessionID)
	return err
}

// MCPCall says whether the loop's /mcp takes a tool call that names
// sessionID as its session: only the loop's own session (MCPSession),
// while a turn of it is in flight or within turnGrace of one, since a
// process left running in the VM is the likeliest misuse between wakes
// (ADR-0061). A pending start's session is in its first turn.
func (cloud *Runtime) MCPCall(ctx context.Context, loopID, sessionID string) error {
	pending, err := cloud.boundSession(ctx, loopID, sessionID)
	if err != nil || pending {
		return err
	}
	current, err := cloud.recorded(loopID)
	if err != nil {
		return err
	}
	// a turn cut off by a hub restart may still run until its bound
	if turn := current.Turn; turn != nil && (!turn.CutOff || time.Now().UnixMilli() < turn.Until) {
		return nil
	}
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	if cloud.waits.stops[loopID] != nil || time.Since(cloud.waits.ended[loopID]) < turnGrace {
		return nil
	}
	return errNoTurn
}

// boundSession refuses sessionID unless it is the loop's session: the one
// a pending start's CLI printed, which pending reports, or the recorded
// one.
func (cloud *Runtime) boundSession(ctx context.Context, loopID, sessionID string) (pending bool, err error) {
	sessionID = sessionAddress(sessionID)
	cloud.waits.mu.Lock()
	start := cloud.waits.starts[loopID]
	cloud.waits.mu.Unlock()
	if start != nil {
		deadline := time.NewTimer(time.Until(start.deadline))
		defer deadline.Stop()
		select {
		case <-start.seen:
		case <-deadline.C:
			return true, errNotTheSession
		case <-ctx.Done():
			return true, ctx.Err()
		}
		cloud.waits.mu.Lock()
		viewed := start.viewed
		cloud.waits.mu.Unlock()
		if sessionID != viewed {
			return true, errNotTheSession
		}
		return true, nil
	}
	current, err := cloud.recorded(loopID)
	if err != nil {
		return false, err
	}
	if current.SessionID == "" || sessionID != current.SessionID {
		return false, errNotTheSession
	}
	return false, nil
}

// cliSession is a loop's cloud session driven through the CLI, seen by the
// runner as the claude process it would be in a workstation.
type cliSession struct {
	cloud  *Runtime
	loopID string
	prompt string
	source runtime.CloudStart
	bounds Bounds
	// env is the loop's connections as the spec carries them, which a
	// start hands to the session.
	env map[string]string

	// claudeSession is the hub's session id, which the events carry;
	// sessionID is the cloud session, "" until the first Send starts one.
	claudeSession string
	sessionID     string
	lost          bool

	sends  chan string
	events chan claude.Event
	ctx    context.Context
	stop   context.CancelFunc

	mu      sync.Mutex
	stopped bool
	killed  bool
	exited  chan struct{}
	exit    claude.ExitInfo
}

func (session *cliSession) start() {
	session.sends = make(chan string, 1)
	session.events = make(chan claude.Event, 64)
	session.exited = make(chan struct{})
	session.ctx, session.stop = context.WithCancel(context.Background())
	go session.run()
}

// Send starts a turn, run from the session's own goroutine.
func (session *cliSession) Send(text string) error {
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

func (session *cliSession) Events() <-chan claude.Event { return session.events }

// CloseStdin ends the Proc between turns; the cloud session idles on.
func (session *cliSession) CloseStdin() error {
	session.close(false)
	return nil
}

// Kill ends the Proc. The CLI can't interrupt a cloud session, so a turn
// in flight runs on in the VM (ADR-0059).
func (session *cliSession) Kill() error {
	session.close(true)
	return nil
}

func (session *cliSession) close(kill bool) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.stopped {
		return
	}
	session.stopped, session.killed = true, kill
	session.stop()
}

func (session *cliSession) Wait() claude.ExitInfo {
	<-session.exited
	return session.exit
}

// PID is 0: the session is no process of the hub's.
func (session *cliSession) PID() int { return 0 }

func (session *cliSession) run() {
	defer close(session.exited)
	defer close(session.events)
	if session.lost {
		session.exit = claude.ExitInfo{Code: 1, Stderr: sessionLost + session.claudeSession}
		return
	}
	for {
		select {
		case <-session.ctx.Done():
			session.mu.Lock()
			if session.killed {
				session.exit = claude.ExitInfo{Code: -1, Stderr: "killed"}
			}
			session.mu.Unlock()
			return
		case text := <-session.sends:
			session.turn(text)
		}
	}
}

// turn starts the session with text, or sends text into it, and passes on
// the turn its Stop post reads back. The wait for that post is registered
// before the CLI runs, so a turn that ends at once is not missed. The turn
// is recorded from the send until its post, or, when the session is closed
// first, as cut off until its quiet bound.
func (session *cliSession) turn(text string) {
	stops := session.cloud.waits.awaitStop(session.loopID)
	defer session.cloud.waits.forgetStop(session.loopID, stops)
	sent := time.Now()
	var err error
	if session.sessionID == "" {
		err = session.startSession(text)
	} else {
		err = session.send(text)
	}
	if err != nil {
		session.fail(err.Error())
		return
	}
	inFlight := session.cloud.beginTurn(session.loopID, sent, session.bounds.Quiet)
	backstop := time.NewTimer(session.bounds.Quiet)
	defer backstop.Stop()
	select {
	case <-session.ctx.Done():
		session.cloud.cutOffTurn(session.loopID, inFlight)
	case <-backstop.C:
		session.cloud.endTurn(session.loopID, inFlight.Since)
		session.fail(fmt.Sprintf("the session posted no turn for %s", session.bounds.Quiet))
	case arrival := <-stops:
		session.cloud.endTurn(session.loopID, inFlight.Since)
		if arrival.overBound {
			session.fail("the turn's Stop post is over its bound and was not read")
			return
		}
		events, err := turnEvents(arrival.post, session.claudeSession, session.bounds)
		if err != nil {
			session.fail(err.Error())
			return
		}
		for _, event := range events {
			if !session.pass(event) {
				return
			}
		}
	}
}

// startSession runs `claude --cloud` on a terminal in a fresh start
// directory, and waits for the session's SessionStart hook to post the
// session's id. It records the session, then ends the local CLI and
// removes the directory. A CLI that exits with an error first, or a post
// that doesn't come within the bound, fails the start with the CLI's last
// line. A start whose CLI exited cleanly, and whose session then posted
// nothing at all in the bound, is told to Unreached.
func (session *cliSession) startSession(text string) error {
	cli := session.cloud.CLI
	within := cli.StartWithin
	if within <= 0 {
		within = defaultStartWithin
	}
	ctx, cancel := context.WithTimeout(session.ctx, within)
	defer cancel()
	settings, err := startSettings(session.source.EnvironmentID)
	if err != nil {
		return err
	}
	if err := session.cloud.confirmEnvironment(ctx, session.source); err != nil {
		return err
	}
	if err := cli.ownDir(); err != nil {
		return err
	}
	dir, err := makeStartDir(ctx, cli.Dir, session.source)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	run, err := session.cloud.newCLIRun(ctx, session.source.Login, dir)
	if err != nil {
		return err
	}
	defer run.remove()

	answer, err := sessionStartAnswer(exportLines(session.env), session.prompt)
	if err != nil {
		return err
	}
	pending := session.cloud.waits.awaitStart(session.loopID, within, answer)
	defer session.cloud.waits.forgetStart(session.loopID, pending)
	// --ref starts the session from the branch on GitHub: without it the
	// CLI bundles the start directory, which has nothing to bundle (run R
	// on #1180). CCR_ENABLE_BUNDLE stays unset, so nothing is uploaded. Its
	// debug log says which environment it picked (#1232).
	cmd := exec.Command(cli.Binary, "--cloud", text, "--permission-mode", "auto", "--settings", settings, "--ref", session.source.Branch, "--debug-file", run.debugLog)
	cmd.Dir, cmd.Env = run.dir, run.env
	terminal, err := startOnTerminal(cmd)
	if err != nil {
		return fmt.Errorf("cloud: starting the session: %w", err)
	}
	screen := &lastLine{line: func(line string) {
		if sessionID, ok := viewedSession(line); ok {
			session.cloud.waits.viewed(pending, sessionID)
		}
	}}
	read := make(chan struct{})
	go func() {
		defer close(read)
		_, _ = io.Copy(screen, terminal)
	}()
	var exitErr error
	exited := make(chan struct{})
	go func() {
		exitErr = cmd.Wait()
		close(exited)
	}()
	defer func() {
		killTerminal(cmd)
		<-exited
		_ = terminal.Close()
		<-read
	}()

	// A CLI that exits cleanly has started the session: CLI 2.1.310 ends
	// once the session is created, before its VM posts SessionStart, so
	// the start waits on for that post within its bound (#1102).
	watched := make(chan struct{})
	defer close(watched)
	misplaced := watchEnvironment(run.debugLog, session.source.EnvironmentID, watched)
	cliExited := exited
	for {
		select {
		case sessionID := <-pending.session:
			session.sessionID = sessionID
			err := session.cloud.update(session.loopID, func(s *state) {
				s.SessionID, s.ClaudeSession, s.EnvironmentID = sessionID, session.claudeSession, ""
				s.SessionURL = sessionPages + sessionID
				s.Source = sourceKey(session.source)
			})
			session.cloud.waits.mu.Lock()
			delete(session.cloud.waits.binding, session.loopID)
			session.cloud.waits.mu.Unlock()
			return err
		case picked := <-misplaced:
			return session.misplaced(pending, picked)
		case <-cliExited:
			if exitErr == nil {
				cliExited = nil
				continue
			}
			// what it last showed may still be on its way through the terminal
			select {
			case <-read:
			case <-time.After(time.Second):
			}
			// The timeline shows only that the turn errored, so the hub's log
			// says why (#1102), without the CLI's line: its screen can show
			// the wake's text.
			slog.Default().Warn("cloud: a start failed: the CLI exited before the session started", "loop", session.loopID, "exit", exitErr)
			return fmt.Errorf("cloud: the CLI exited (%v) before the session started: %s", exitErr, screen.String())
		case <-ctx.Done():
			if session.ctx.Err() != nil {
				return session.ctx.Err()
			}
			heard := session.cloud.waits.heard(pending)
			slog.Default().Warn("cloud: a start failed: no session started in time", "loop", session.loopID, "within", within, "cli_exited", cliExited == nil, "session_posted", heard)
			// Only a CLI that exited cleanly created the session, so only then
			// is its silence the session's word on the address. A CLI still
			// running has created none yet (#1102).
			if cliExited == nil && !heard {
				if unreached := session.cloud.Unreached; unreached != nil {
					unreached(session.loopID, within)
				}
				return fmt.Errorf("cloud: the session never reached the hub within %s: %s", within, screen.String())
			}
			return fmt.Errorf("cloud: no session started within %s: %s", within, screen.String())
		}
	}
}

// misplaced fails a start whose CLI picked environment picked rather than
// the loop's, and tells Misplaced, so the loop holds until a setup check
// passes rather than making a stray session at every wake (#1232). The
// local CLI is ended on return, which usually comes before it creates the
// session.
func (session *cliSession) misplaced(pending *pendingStart, picked string) error {
	session.cloud.waits.mu.Lock()
	viewed := pending.viewed
	session.cloud.waits.mu.Unlock()
	detail := misplacedDetail(session.source.EnvironmentID, picked, viewed)
	slog.Default().Warn("cloud: a start failed: the CLI picked another environment", "loop", session.loopID, "environment", session.source.EnvironmentID, "picked", picked, "session", viewed)
	if tell := session.cloud.Misplaced; tell != nil {
		tell(session.loopID, detail)
	}
	return fmt.Errorf("cloud: %s", detail)
}

// misplacedDetail says that the CLI picked environment picked, not want,
// and what the operator does about the session it made, viewed, or may
// have made when "".
func misplacedDetail(want, picked, viewed string) string {
	made := "check claude.ai for a session it may have made, and archive it"
	if viewed != "" {
		made = "archive session " + viewed + " on claude.ai"
	}
	return fmt.Sprintf("the CLI picked environment %s, not the loop's %s, which the account doesn't hold: %s, and name one of the account's environments",
		picked, want, made)
}

// send runs `claude -p --cloud` with text, which only acknowledges the
// send: the turn comes back by the Stop hook. A send the CLI refuses fails
// the turn with what it said.
func (session *cliSession) send(text string) error {
	ctx, cancel := context.WithTimeout(session.ctx, sendWithin)
	defer cancel()
	run, err := session.cloud.newCLIRun(ctx, session.source.Login, "")
	if err != nil {
		return err
	}
	defer run.remove()
	cmd := exec.CommandContext(ctx, session.cloud.CLI.Binary, "-p", text, "--cloud", session.sessionID, "--output-format", "json")
	cmd.Dir, cmd.Env = run.dir, run.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	ran := cmd.Run()
	var ack struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ack) != nil || !ack.OK || ran != nil {
		said := strings.TrimSpace(stdout.String() + " " + stderr.String())
		slog.Default().Warn("cloud: a send failed: the CLI refused it", "loop", session.loopID, "exit", ran)
		return fmt.Errorf("cloud: the CLI refused the send (%v): %s", ran, snippet([]byte(said)))
	}
	return nil
}

// cliRun is where one CLI run is made: its environment, and the directory
// it runs in.
type cliRun struct {
	env []string
	dir string
	// debugLog is where a start asks the CLI for its debug log, in the
	// run's own directory, which remove takes with it: the log carries the
	// wake's text, and names the environment the CLI picked (#1232).
	debugLog string
	// remove removes the run's config dir, and its own directory with the
	// debug log in it, once the run ends.
	remove func()
}

// newCLIRun makes a CLI run under the Claude login called login, "" for
// the default: the CLI's own environment, a fresh config dir
// and the account's token. The config dir holds no credential: only the
// onboarding flag, the account's organization, which the sign-in reads,
// and the trust of home when there is one (ADR-0059). The run is made in
// home, or with none in an empty directory of its own: never the hub's
// working directory or the shared temp dir, whose project settings, hooks
// among them, the CLI would load and run.
func (cloud *Runtime) newCLIRun(ctx context.Context, login, home string) (cliRun, error) {
	account, err := cloud.Account(ctx, login)
	if err != nil {
		return cliRun{}, err
	}
	if err := cloud.CLI.ownDir(); err != nil {
		return cliRun{}, err
	}
	scratch, err := os.MkdirTemp(cloud.CLI.Dir, "spool-cli-*")
	if err != nil {
		return cliRun{}, err
	}
	configDir := filepath.Join(scratch, "config")
	run := cliRun{dir: home, debugLog: filepath.Join(scratch, "debug.log"), remove: func() {
		removeConfig(configDir)
		warnTokenIn(filepath.Join(scratch, "debug.log"), account.Token)
		_ = os.RemoveAll(scratch)
	}}
	if run.dir == "" {
		run.dir = filepath.Join(scratch, "work")
	}
	config := map[string]any{
		"hasCompletedOnboarding": true,
		"oauthAccount":           map[string]string{"organizationUuid": account.OrgUUID},
	}
	if home != "" {
		config["projects"] = map[string]any{home: map[string]bool{"hasTrustDialogAccepted": true}}
	}
	raw, err := json.Marshal(config)
	if err == nil {
		err = errors.Join(os.Mkdir(configDir, 0o700), os.MkdirAll(run.dir, 0o700))
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(configDir, ".claude.json"), raw, 0o600)
	}
	if err != nil {
		run.remove()
		return cliRun{}, err
	}
	run.env = append(append([]string(nil), cloud.CLI.Env...),
		"CLAUDE_CONFIG_DIR="+configDir, "CLAUDE_CODE_OAUTH_TOKEN="+account.Token)
	return run, nil
}

// fail ends the turn as an error that says what went wrong.
func (session *cliSession) fail(reason string) {
	raw, _ := json.Marshal(map[string]any{
		"type":       "result",
		"subtype":    "error_during_execution",
		"is_error":   true,
		"result":     "cloud runtime: " + reason,
		"session_id": session.claudeSession,
	})
	session.pass(claude.DecodeEvent(raw))
}

func (session *cliSession) pass(event claude.Event) bool {
	select {
	case session.events <- event:
		return true
	case <-session.ctx.Done():
		return false
	}
}

// lastLine keeps the last non-empty line a terminal showed, for an error
// to quote. A TUI redraws with escape sequences, which it drops. line,
// when set, is told each line as it ends.
type lastLine struct {
	mu      sync.Mutex
	partial []byte
	last    string
	line    func(string)
}

func (screen *lastLine) Write(p []byte) (int, error) {
	screen.mu.Lock()
	defer screen.mu.Unlock()
	for _, b := range p {
		if b == '\n' || b == '\r' {
			if line := strings.TrimSpace(stripEscapes(screen.partial)); line != "" {
				screen.last = line
				if screen.line != nil {
					screen.line(line)
				}
			}
			screen.partial = screen.partial[:0]
			continue
		}
		if len(screen.partial) < 4096 {
			screen.partial = append(screen.partial, b)
		}
	}
	return len(p), nil
}

func (screen *lastLine) String() string {
	screen.mu.Lock()
	defer screen.mu.Unlock()
	if line := strings.TrimSpace(stripEscapes(screen.partial)); line != "" {
		return line
	}
	if screen.last == "" {
		return "(the CLI showed nothing)"
	}
	return screen.last
}

// stripEscapes drops a terminal's escape sequences, CSI, OSC and the
// two-byte ones such as a charset choice, and other control bytes. A
// window title set through OSC is dropped with its text (#1102).
func stripEscapes(raw []byte) string {
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		switch {
		case raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == '[':
			i += 2
			for i < len(raw) && (raw[i] < 0x40 || raw[i] > 0x7e) {
				i++
			}
		case raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == ']':
			// to its BEL, or its ESC \
			for i += 2; i < len(raw) && raw[i] != 0x07; i++ {
				if raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == '\\' {
					i++
					break
				}
			}
		case raw[i] == 0x1b && i+2 < len(raw) && strings.IndexByte("()*+", raw[i+1]) >= 0:
			i += 2
		case raw[i] == 0x1b && i+1 < len(raw):
			i++
		case raw[i] < 0x20 || raw[i] == 0x7f:
		default:
			out.WriteByte(raw[i])
		}
	}
	return out.String()
}

// cliLeftovers are what the CLI may write into its config dir that the
// hub never gave it: a credential, or a project's transcript, shell
// snapshot or debug log, which can carry the envelope the run was sent
// (ADR-0059).
var cliLeftovers = []string{".credentials.json", "projects", "shell-snapshots", "debug"}

// removeConfig removes a run's config dir, and logs any of cliLeftovers
// it found there first, by name only.
func removeConfig(configDir string) {
	if found := leftovers(configDir); len(found) > 0 {
		slog.Default().Warn("the claude CLI left state in its config dir; removed", "found", found)
	}
	_ = os.RemoveAll(configDir)
}

// leftovers names each of cliLeftovers in configDir.
func leftovers(configDir string) []string {
	var found []string
	for _, name := range cliLeftovers {
		if _, err := os.Lstat(filepath.Join(configDir, name)); err == nil {
			found = append(found, name)
		}
	}
	return found
}

// CheckAccount is ADR-0044's login check on the account's token, run as a
// cloud session's CLI runs are, in a config dir of its own (ADR-0059): one
// haiku answer to a one-word prompt, which spends a sliver of the plan
// and reaches no session. It is ErrNoAccount with no account signed in.
func (cloud *Runtime) CheckAccount(ctx context.Context) (claude.LoginCheck, error) {
	run, err := cloud.newCLIRun(ctx, "", "")
	if err != nil {
		return claude.LoginCheck{}, err
	}
	defer run.remove()
	cmd := exec.Command(cloud.CLI.Binary, claude.LoginCheckArgs()...)
	cmd.Dir, cmd.Env = run.dir, run.env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return claude.LoginCheck{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return claude.LoginCheck{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return claude.LoginCheck{}, err
	}
	if err := cmd.Start(); err != nil {
		return claude.LoginCheck{}, fmt.Errorf("cloud: starting %s: %w", cloud.CLI.Binary, err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	return claude.CheckLogin(ctx, claude.Attach(stdin, stdout, stderr))
}

// Prepare makes Dir, or takes it as it is when it is a 0700 directory
// already, and removes everything in it: a hub that stopped mid-start
// left that start's directories there, the CLI's config dir among them. It
// is the hub's to call once at boot, before any start. Dir is in the
// shared temp dir, so anything else at that path is refused: a directory
// another user made there would hand them the runs.
func (cli *CLI) Prepare() (removed int, err error) {
	if err := cli.ownDir(); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(cli.Dir)
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(cli.Dir, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// ownDir makes Dir, or checks the directory there is a 0700 one this user
// owns. It runs before every run, not just at boot: a temp dir cleaner may
// remove Dir while the hub idles, and another user could make it again in
// its place.
func (cli *CLI) ownDir() error {
	if err := os.Mkdir(cli.Dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(cli.Dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("cloud: %s is not a directory only this user can open (%s); remove it", cli.Dir, info.Mode())
	}
	// a hub running as root can write into another user's 0700 directory,
	// and that user, its owner, could swap what the hub puts there
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("cloud: %s belongs to another user; remove it", cli.Dir)
	}
	return nil
}

// startSettings is the --settings a start runs with: the loop's
// environment (ADR-0061), built by the encoder, and the deny rules the
// setup script writes too. The first live start found the VM's copy of
// the rules holding, and couldn't tell whether this one reaches the
// session (#1190). With no environment there is no start, since the
// CLI would pick one of the account's environments by itself.
func startSettings(environmentID string) (string, error) {
	if environmentID == "" {
		return "", errors.New("cloud: the loop has no environment")
	}
	raw, err := json.Marshal(map[string]any{
		"remote":      map[string]string{"defaultEnvironmentId": environmentID},
		"permissions": map[string]any{"deny": deniedTools()},
	})
	return string(raw), err
}

// makeStartDir makes a start directory under parent (ADR-0061): a fresh
// 0700 directory holding an empty git repo on source's branch, its
// origin source's repo on GitHub. It holds no file of the hub's and no
// clone, so the CLI sends the repo as the session's source. git runs by
// argument list, with the operator's git config kept out, on a repo and
// branch it checks again here, whatever its caller checked (gitSafe).
// The caller removes the directory once the start ends.
func makeStartDir(ctx context.Context, parent string, source runtime.CloudStart) (dir string, err error) {
	if !gitSafe(source) {
		return "", fmt.Errorf("cloud: %q on %q is not a repo and branch to start from", source.Repo, source.Branch)
	}
	dir, err = os.MkdirTemp(parent, "spool-start-*")
	if err != nil {
		return "", err
	}
	for _, step := range []struct {
		name string
		args []string
	}{
		{"init", []string{"init", "-q", "-b", source.Branch}},
		{"remote", []string{"remote", "add", "origin", "https://github.com/" + source.Repo + ".git"}},
	} {
		cmd := exec.CommandContext(ctx, "git", step.args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("cloud: making the start directory: git %s: %w: %s", step.name, err, strings.TrimSpace(stderr.String()))
		}
	}
	return dir, nil
}

// gitSafe reports whether source's repo and branch can go into the start
// directory's git arguments: a branch that doesn't start with "-", so git
// can't read it as a flag, and a repo that is one owner/name of letters,
// digits, '.', '-' and '_', its owner not starting with "-" and its name
// not "." or "..". It is the runtime's own guard, since the seam keeps it
// from the store: looser than CloudSetup.Check, so it refuses no setup
// the store took, but enough that git reads both as values.
func gitSafe(source runtime.CloudStart) bool {
	owner, name, found := strings.Cut(source.Repo, "/")
	if source.Branch == "" || source.Branch[0] == '-' || !found || owner == "" || name == "" ||
		owner[0] == '-' || name == "." || name == ".." {
		return false
	}
	for _, char := range owner + name {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && !strings.ContainsRune("._-", char) {
			return false
		}
	}
	return true
}

// recorded is the loop's state, with the session a SessionStart post has
// just bound in its place until the start has recorded it.
func (cloud *Runtime) recorded(loopID string) (state, error) {
	current, err := cloud.load(loopID)
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	if bound := cloud.waits.binding[loopID]; bound != "" {
		current.SessionID = bound
	}
	return current, err
}

// warnTokenIn logs, by name only, a debug log at path that holds token.
// No CLI has been seen writing it there, but the CLI runs with the token
// in its environment, and a later one could start: the log is removed
// with the run either way, so this only makes sure it gets noticed.
func warnTokenIn(path, token string) {
	if token == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err == nil && bytes.Contains(raw, []byte(token)) {
		slog.Default().Warn("the claude CLI wrote the account's token to its debug log; removed", "file", filepath.Base(path))
	}
}
