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
)

// CLI is the claude CLI cloud sessions run through, and where its runs
// are made (ADR-0059): `claude --cloud` under a pseudo-terminal starts a
// session, `claude -p --cloud` sends into it, and each turn is read back
// from what the loop home's Stop hook posts.
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
// SessionStart and a turn's Stop, each by loop.
type hookWaits struct {
	mu     sync.Mutex
	starts map[string]*pendingStart
	stops  map[string]chan stopArrival
}

// pendingStart is a start of a loop waiting for its session's id: it takes
// one post, before its deadline. It answers that post with exports, the
// loop's connections.
type pendingStart struct {
	deadline time.Time
	session  chan string
	exports  []byte
}

// stopArrival is a Stop post as the turn in flight takes it.
type stopArrival struct {
	post      StopPost
	overBound bool
}

func (waits *hookWaits) awaitStart(loopID string, within time.Duration, exports []byte) *pendingStart {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.starts == nil {
		waits.starts = map[string]*pendingStart{}
	}
	start := &pendingStart{deadline: time.Now().Add(within), session: make(chan string, 1), exports: exports}
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

// SessionStart takes a SessionStart post and answers it with the loop's
// connections as export lines (ADR-0059). A startup post's session id is
// recorded as the loop's while a start of it is pending, within its bound,
// once. Any other post is refused: the loop can read its token and
// post, and must not rebind its wakes into another session or read the
// connections at will. An answer is told to HandOff, and so is a refused
// post from the loop's own session, which may have lost them.
// Each answer is logged, without its values.
func (cloud *Runtime) SessionStart(_ context.Context, loopID, sessionID string, hook []byte) ([]byte, error) {
	sessionID = sessionAddress(sessionID)
	source := hookSource(hook)
	exports, err := cloud.answerSessionStart(loopID, sessionID, source)
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
	return exports, err
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

func (cloud *Runtime) answerSessionStart(loopID, sessionID, source string) ([]byte, error) {
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	if source != sourceStartup {
		return nil, fmt.Errorf("cloud: a SessionStart post from %q is never answered", source)
	}
	start := cloud.waits.starts[loopID]
	if start == nil || time.Now().After(start.deadline) {
		return nil, errNoHandOff
	}
	delete(cloud.waits.starts, loopID)
	start.session <- sessionID
	return start.exports, nil
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

// cliSession is a loop's cloud session driven through the CLI, seen by the
// runner as the claude process it would be in a workstation.
type cliSession struct {
	cloud  *Runtime
	loopID string
	prompt string
	hub    hubEndpoint
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
	settings, err := session.cloud.startSettings(ctx)
	if err != nil {
		return err
	}
	if err := cli.ownDir(); err != nil {
		return err
	}
	home, err := BuildHome(ctx, cli.Dir, Home{MCPURL: session.hub.url, Token: session.hub.token, Prompt: session.prompt})
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(home) }()
	run, err := session.cloud.newCLIRun(ctx, home)
	if err != nil {
		return err
	}
	defer run.remove()

	pending := session.cloud.waits.awaitStart(session.loopID, within, exportLines(session.env))
	defer session.cloud.waits.forgetStart(session.loopID, pending)
	cmd := exec.Command(cli.Binary, "--cloud", text, "--permission-mode", "auto", "--settings", settings)
	// The CLI uploads the home only when CCR_ENABLE_BUNDLE is set or a
	// feature flag says to, and the hub's runs fetch no feature flags
	// (CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC): without it the session
	// starts in an empty sandbox, with no MCP server and no hooks (#1094).
	cmd.Dir, cmd.Env = run.dir, append(run.env, "CCR_ENABLE_BUNDLE=1")
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
			s.SessionID, s.ClaudeSession, s.EnvironmentID = sessionID, session.claudeSession, ""
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
	run, err := session.cloud.newCLIRun(ctx, "")
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
		return fmt.Errorf("cloud: the CLI refused the send (%v): %s", ran, snippet([]byte(said)))
	}
	return nil
}

// cliRun is where one CLI run is made: its environment, and the directory
// it runs in.
type cliRun struct {
	env []string
	dir string
	// remove removes the run's config dir, and its own empty directory
	// when it has one, once the run ends.
	remove func()
}

// newCLIRun makes a CLI run: the CLI's own environment, a fresh config dir
// and the account's token. The config dir holds no credential: only the
// onboarding flag, the account's organization, which the sign-in reads,
// and the trust of home when there is one (ADR-0059). The run is made in
// home, or with none in an empty directory of its own: never the hub's
// working directory or the shared temp dir, whose project settings, hooks
// among them, the CLI would load and run.
func (cloud *Runtime) newCLIRun(ctx context.Context, home string) (cliRun, error) {
	account, err := cloud.Account(ctx)
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
	run := cliRun{dir: home, remove: func() {
		removeConfig(configDir)
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
	run, err := cloud.newCLIRun(ctx, "")
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
// left that start's loop home there, and it holds a token (ADR-0059). It
// is the hub's to call once at boot, before any start. Dir is in the
// shared temp dir, so anything else at that path is refused: a directory
// another user made there would hand them the homes.
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

// startSettings is the --settings a start runs with: the environment the
// session starts in (ADR-0059). With none chosen there is no start, since
// the CLI would pick one of the account's environments by itself.
func (cloud *Runtime) startSettings(ctx context.Context) (string, error) {
	id := ""
	if cloud.EnvironmentID != nil {
		var err error
		if id, err = cloud.EnvironmentID(ctx); err != nil {
			return "", err
		}
	}
	if id == "" {
		return "", errors.New("cloud: no environment is chosen in Settings → Cloud")
	}
	raw, err := json.Marshal(map[string]any{"remote": map[string]string{"defaultEnvironmentId": id}})
	return string(raw), err
}
