package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// CLI drives cloud sessions through the claude CLI and the loop home's
// hooks, on documented surfaces (ADR-0059): `claude --cloud` under a
// pseudo-terminal starts a session, `claude -p --cloud` sends into it, and
// each turn is read back from what the Stop hook posts.
//
// It is temporary: until slice 2 of ADR-0059 (#977) makes it the cloud
// runtime's only path, the hub sets it only under its temporary flag, and
// the sessions client runs every cloud loop otherwise.
type CLI struct {
	// Binary is the claude CLI.
	Binary string
	// Dir is where each run's loop home and config dir are made, outside
	// the data directory, and removed once the run ends.
	Dir string
	// Env is the CLI's environment: the hub's, with no credential of its
	// own. Each run adds its config dir and the account's token.
	Env []string
	// StartWithin bounds a start from running the CLI to the session's
	// SessionStart post; two minutes when zero.
	StartWithin time.Duration
}

// The bound on a start, and on a send's CLI run.
const (
	defaultStartWithin = 2 * time.Minute
	sendWithin         = 2 * time.Minute
)

// hookWaits are the hook posts the runtime is waiting on: a start's
// SessionStart and a turn's Stop, each by loop.
type hookWaits struct {
	mu     sync.Mutex
	starts map[string]*pendingStart
	stops  map[string]chan stopArrival
}

// pendingStart is a start of a loop waiting for its session's id: it takes
// one post, before its deadline.
type pendingStart struct {
	deadline time.Time
	session  chan string
}

// stopArrival is a Stop post as the turn in flight takes it.
type stopArrival struct {
	post      StopPost
	overBound bool
}

func (waits *hookWaits) awaitStart(loopID string, within time.Duration) *pendingStart {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.starts == nil {
		waits.starts = map[string]*pendingStart{}
	}
	start := &pendingStart{deadline: time.Now().Add(within), session: make(chan string, 1)}
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
}

// SessionStart takes a SessionStart post: the session's id, recorded as
// the loop's while a start of it is pending, within its bound, once
// (ADR-0059). At any other time it is refused: the loop can read its token
// and post, and must not rebind its wakes into another session.
func (cloud *Runtime) SessionStart(_ context.Context, loopID, sessionID string, _ []byte) error {
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	start := cloud.waits.starts[loopID]
	if start == nil || time.Now().After(start.deadline) {
		return errors.New("cloud: no start of this loop is pending")
	}
	delete(cloud.waits.starts, loopID)
	start.session <- sessionID
	return nil
}

// Stop takes a Stop post: it ends the loop's turn in flight, once. With no
// turn in flight it is refused.
func (cloud *Runtime) Stop(_ context.Context, loopID string, hook, lines []byte, overBound bool) error {
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	stops := cloud.waits.stops[loopID]
	if stops == nil {
		return errors.New("cloud: no turn of this loop is in flight")
	}
	delete(cloud.waits.stops, loopID)
	stops <- stopArrival{post: StopPost{Hook: hook, Lines: lines}, overBound: overBound}
	return nil
}

// startCLI hands back the loop's session as a Proc driven through the CLI,
// as Start does for the sessions client: a fresh hub session starts a cloud
// session with its first message, and a resume carries on in the one it ran
// in, under the token it was started with.
func (cloud *Runtime) startCLI(spec runtime.Spec) (runtime.Proc, error) {
	hub, err := hubFrom(spec)
	if err != nil {
		return nil, err
	}
	current, err := cloud.load(spec.LoopID)
	if err != nil {
		return nil, err
	}
	session := &cliSession{
		cloud:  cloud,
		loopID: spec.LoopID,
		prompt: spec.AppendSystemPrompt,
		hub:    hub,
		bounds: cloud.Bounds.withDefaults(),
	}
	switch {
	case spec.SessionID != "":
		session.claudeSession = spec.SessionID
	case spec.ResumeID == current.ClaudeSession && current.SessionID != "" && current.SessionToken == tokenHash(hub.token):
		session.claudeSession = spec.ResumeID
		session.sessionID = current.SessionID
	default:
		session.claudeSession = spec.ResumeID
		session.lost = true
	}
	session.start()
	return session, nil
}

// cliSession is a loop's cloud session driven through the CLI, seen by the
// runner as the claude process it would be in a workstation.
type cliSession struct {
	cloud  *Runtime
	loopID string
	prompt string
	hub    hubEndpoint
	bounds Bounds

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
// before the CLI runs, so a turn that ends at once is not missed.
func (session *cliSession) turn(text string) {
	stops := session.cloud.waits.awaitStop(session.loopID)
	defer session.cloud.waits.forgetStop(session.loopID, stops)
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
	backstop := time.NewTimer(session.bounds.Quiet)
	defer backstop.Stop()
	select {
	case <-session.ctx.Done():
	case <-backstop.C:
		session.fail(fmt.Sprintf("the session posted no turn for %s", session.bounds.Quiet))
	case arrival := <-stops:
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

// startSession runs `claude --cloud` on a terminal in a fresh loop home,
// and waits for the home's SessionStart hook to post the session's id. It
// records the session, then ends the local CLI and removes the home, which
// holds the loop's token. A CLI that exits first, or a post that doesn't
// come within the bound, fails the start with the CLI's last line.
func (session *cliSession) startSession(text string) error {
	cli := session.cloud.CLI
	within := cli.StartWithin
	if within <= 0 {
		within = defaultStartWithin
	}
	ctx, cancel := context.WithTimeout(session.ctx, within)
	defer cancel()
	if err := os.MkdirAll(cli.Dir, 0o700); err != nil {
		return err
	}
	home, err := BuildHome(ctx, cli.Dir, Home{MCPURL: session.hub.url, Token: session.hub.token, Prompt: session.prompt})
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(home) }()
	env, removeConfig, err := session.cloud.cliEnv(ctx, home)
	if err != nil {
		return err
	}
	defer removeConfig()

	pending := session.cloud.waits.awaitStart(session.loopID, within)
	defer session.cloud.waits.forgetStart(session.loopID, pending)
	cmd := exec.Command(cli.Binary, "--cloud", text, "--permission-mode", "auto")
	cmd.Dir, cmd.Env = home, env
	terminal, err := startOnTerminal(cmd)
	if err != nil {
		return fmt.Errorf("cloud: starting the session: %w", err)
	}
	screen := &lastLine{}
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

	select {
	case sessionID := <-pending.session:
		session.sessionID = sessionID
		return session.cloud.update(session.loopID, func(s *state) {
			s.SessionID, s.ClaudeSession, s.AfterID = sessionID, session.claudeSession, ""
			s.SessionURL = sessionPages + sessionID
			s.SessionToken = tokenHash(session.hub.token)
		})
	case <-exited:
		// what it last showed may still be on its way through the terminal
		select {
		case <-read:
		case <-time.After(time.Second):
		}
		return fmt.Errorf("cloud: the CLI exited (%v) before the session started: %s", exitErr, screen.String())
	case <-ctx.Done():
		if session.ctx.Err() != nil {
			return session.ctx.Err()
		}
		return fmt.Errorf("cloud: no session started within %s: %s", within, screen.String())
	}
}

// send runs `claude -p --cloud` with text, which only acknowledges the
// send: the turn comes back by the Stop hook. A send the CLI refuses fails
// the turn with what it said.
func (session *cliSession) send(text string) error {
	ctx, cancel := context.WithTimeout(session.ctx, sendWithin)
	defer cancel()
	env, removeConfig, err := session.cloud.cliEnv(ctx, "")
	if err != nil {
		return err
	}
	defer removeConfig()
	cmd := exec.CommandContext(ctx, session.cloud.CLI.Binary, "-p", text, "--cloud", session.sessionID, "--output-format", "json")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	ran := cmd.Run()
	var ack struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ack) != nil || !ack.OK || ran != nil {
		said := strings.TrimSpace(stdout.String() + " " + stderr.String())
		return fmt.Errorf("cloud: the CLI refused the send (%v): %s", ran, snippet([]byte(said)))
	}
	return nil
}

// cliEnv is a CLI run's environment: the CLI's own, a fresh config dir and
// the account's token. The config dir holds no credential: only the
// onboarding flag, the account's organization, which the sign-in reads,
// and the trust of home when there is one (ADR-0059). The caller removes it
// once the run ends.
func (cloud *Runtime) cliEnv(ctx context.Context, home string) (env []string, remove func(), err error) {
	account, err := cloud.Client.Account(ctx)
	if err != nil {
		return nil, nil, err
	}
	configDir, err := os.MkdirTemp(cloud.CLI.Dir, "spool-cli-*")
	if err != nil {
		return nil, nil, err
	}
	remove = func() { _ = os.RemoveAll(configDir) }
	config := map[string]any{
		"hasCompletedOnboarding": true,
		"oauthAccount":           map[string]string{"organizationUuid": account.OrgUUID},
	}
	if home != "" {
		config["projects"] = map[string]any{home: map[string]bool{"hasTrustDialogAccepted": true}}
	}
	raw, err := json.Marshal(config)
	if err == nil {
		err = os.WriteFile(filepath.Join(configDir, ".claude.json"), raw, 0o600)
	}
	if err != nil {
		remove()
		return nil, nil, err
	}
	env = append(append([]string(nil), cloud.CLI.Env...),
		"CLAUDE_CONFIG_DIR="+configDir, "CLAUDE_CODE_OAUTH_TOKEN="+account.Token)
	return env, remove, nil
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
// to quote. A TUI redraws with escape sequences, which it drops.
type lastLine struct {
	mu      sync.Mutex
	partial []byte
	last    string
}

func (screen *lastLine) Write(p []byte) (int, error) {
	screen.mu.Lock()
	defer screen.mu.Unlock()
	for _, b := range p {
		if b == '\n' || b == '\r' {
			if line := strings.TrimSpace(stripEscapes(screen.partial)); line != "" {
				screen.last = line
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

// stripEscapes drops a terminal's CSI sequences and other control bytes.
func stripEscapes(raw []byte) string {
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		switch {
		case raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == '[':
			i += 2
			for i < len(raw) && (raw[i] < 0x40 || raw[i] > 0x7e) {
				i++
			}
		case raw[i] < 0x20 || raw[i] == 0x7f:
		default:
			out.WriteByte(raw[i])
		}
	}
	return out.String()
}
