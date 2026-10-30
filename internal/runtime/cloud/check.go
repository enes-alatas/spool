package cloud

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/enes-alatas/spool/internal/runtime"
)

// A cloud loop's setup is by hand, on claude.ai, so the hub can't read it
// back: it can only start a session there and see whether the session
// reaches it (ADR-0061, #1182). The check starts one session in the loop's
// environment from its repo, as a start does, with a message that asks
// nothing of it. Its SessionStart post, from the session the CLI printed,
// passes the check. The session is never the loop's: it is answered with
// no connection and no prompt, /mcp never takes it, and its Stop post is
// taken and dropped.

// Where a failed check stopped.
const (
	// CheckStageEnvironment: the CLI refused the start, so no session
	// exists: an environment the account doesn't have, or a repo or branch
	// its GitHub connection can't reach. The detail is the CLI's last line.
	CheckStageEnvironment = "environment"
	// CheckStageToken: the session reached the hub, but with no hub token
	// or one the hub doesn't take: the environment's network secret isn't
	// the loop's.
	CheckStageToken = "token"
	// CheckStageUnreached: nothing of the session reached the hub within
	// the start's bound: the hub's public host isn't on the environment's
	// allowed hosts, or the setup script isn't the hub's.
	CheckStageUnreached = "unreached"
	// CheckStageHub: the hub couldn't run the check at all, such as with no
	// account signed in.
	CheckStageHub = "hub"
)

// maxRefusedSessions caps the refused sessions a check keeps: every
// session refused for its token while it runs, the check's among them.
const maxRefusedSessions = 64

// pendingCheck is a check of a loop waiting for its session's
// SessionStart post.
type pendingCheck struct {
	deadline time.Time
	// viewed is the session the CLI printed it had created, "" until the
	// hub reads it; seen is closed then.
	viewed string
	seen   chan struct{}
	// passed is closed when the session's SessionStart post is taken.
	passed chan struct{}
	// refused are the sessions whose hook posts the hub refused for their
	// token, which may include the check's.
	refused []string
	// heard is whether any SessionStart post of the loop came, refused or
	// not: some session in its environment reached the hub.
	heard bool
}

// CheckSetup starts one session from source in its environment, with
// message, and reports where the check stopped: "" when the hub heard from
// the session, or a CheckStage and what it found.
func (cloud *Runtime) CheckSetup(ctx context.Context, loopID string, source runtime.CloudStart, message string) (stage, detail string) {
	within := cloud.CLI.StartWithin
	if within <= 0 {
		within = defaultStartWithin
	}
	ctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	cli := cloud.CLI
	settings, err := startSettings(source.EnvironmentID)
	if err != nil {
		return CheckStageEnvironment, err.Error()
	}
	if !gitSafe(source) {
		return CheckStageEnvironment, "the loop's repo or branch isn't one a start can name"
	}
	if err := cli.ownDir(); err != nil {
		return CheckStageHub, err.Error()
	}
	dir, err := makeStartDir(ctx, cli.Dir, source)
	if err != nil {
		return CheckStageHub, err.Error()
	}
	defer func() { _ = os.RemoveAll(dir) }()
	run, err := cloud.newCLIRun(ctx, dir)
	if err != nil {
		return CheckStageHub, err.Error()
	}
	defer run.remove()

	check := cloud.waits.awaitCheck(loopID, within)
	defer cloud.waits.forgetCheck(loopID, check)
	cmd := exec.Command(cli.Binary, "--cloud", message, "--permission-mode", "auto", "--settings", settings, "--ref", source.Branch)
	cmd.Dir, cmd.Env = run.dir, run.env
	terminal, err := startOnTerminal(cmd)
	if err != nil {
		return CheckStageHub, fmt.Sprintf("starting the CLI: %v", err)
	}
	screen := &lastLine{line: func(line string) {
		if sessionID, ok := viewedSession(line); ok {
			cloud.waits.viewedCheck(check, sessionID)
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

	cliExited := exited
	for {
		select {
		case <-check.passed:
			return "", ""
		case <-cliExited:
			if exitErr == nil {
				cliExited = nil
				continue
			}
			select {
			case <-read:
			case <-time.After(time.Second):
			}
			slog.Default().Warn("cloud: a setup check failed: the CLI refused the start", "loop", loopID, "exit", exitErr)
			return CheckStageEnvironment, fmt.Sprintf("the CLI exited (%v) before a session started: %s", exitErr, screen.String())
		case <-ctx.Done():
			viewed, refused, heard := cloud.waits.checkSaw(check)
			slog.Default().Warn("cloud: a setup check failed: the hub heard nothing it took", "loop", loopID,
				"within", within, "session", viewed, "token_refused", refused, "session_posted", heard)
			switch {
			case refused:
				return CheckStageToken, fmt.Sprintf("session %s reached the hub with no hub token, or one the hub doesn't take: "+
					"set this loop's token as the environment's network secret for the hub's host", viewed)
			case viewed == "":
				return CheckStageEnvironment, fmt.Sprintf("the CLI started no session within %s: %s", within, screen.String())
			}
			return CheckStageUnreached, fmt.Sprintf("session %s posted nothing the hub heard within %s: check that the environment's "+
				"allowed hosts carry the hub's host, and that its setup script is the one this hub shows", viewed, within)
		}
	}
}

// TokenRefused is told of a hook post the hub refused for its token, from
// sessionID: a check whose session it is stopped at the token.
func (cloud *Runtime) TokenRefused(sessionID string) {
	sessionID = sessionAddress(sessionID)
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	for _, check := range cloud.waits.checks {
		if len(check.refused) < maxRefusedSessions && !slices.Contains(check.refused, sessionID) {
			check.refused = append(check.refused, sessionID)
		}
	}
}

func (waits *hookWaits) awaitCheck(loopID string, within time.Duration) *pendingCheck {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.checks == nil {
		waits.checks = map[string]*pendingCheck{}
	}
	check := &pendingCheck{deadline: time.Now().Add(within), seen: make(chan struct{}), passed: make(chan struct{})}
	waits.checks[loopID] = check
	return check
}

func (waits *hookWaits) forgetCheck(loopID string, check *pendingCheck) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if waits.checks[loopID] == check {
		delete(waits.checks, loopID)
	}
}

// viewedCheck records the session a check's CLI printed it had created.
func (waits *hookWaits) viewedCheck(check *pendingCheck, sessionID string) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	if check.viewed == "" {
		check.viewed = sessionID
		close(check.seen)
	}
}

// checkSaw is what a check saw of its session: the session the CLI
// printed, whether the hub refused one of its posts for the token, and
// whether any SessionStart post of the loop came.
func (waits *hookWaits) checkSaw(check *pendingCheck) (viewed string, refused, heard bool) {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	return check.viewed, check.viewed != "" && slices.Contains(check.refused, check.viewed), check.heard
}

// answerCheck takes a startup SessionStart post from the session a check of
// the loop printed, and answers it with nothing for the session: no
// connection and no prompt. ok is false for any other post, which goes on
// to a start's judging. A post that comes before the hub has read the
// CLI's line waits for it within the check's bound, as a start's does.
func (cloud *Runtime) answerCheck(ctx context.Context, loopID, sessionID string) (answer []byte, ok bool) {
	cloud.waits.mu.Lock()
	check := cloud.waits.checks[loopID]
	if check != nil {
		check.heard = true
	}
	cloud.waits.mu.Unlock()
	if check == nil {
		return nil, false
	}
	deadline := time.NewTimer(time.Until(check.deadline))
	defer deadline.Stop()
	select {
	case <-check.seen:
	case <-deadline.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
	cloud.waits.mu.Lock()
	defer cloud.waits.mu.Unlock()
	if sessionID != check.viewed {
		return nil, false
	}
	select {
	case <-check.passed:
	default:
		close(check.passed)
	}
	if cloud.waits.checked == nil {
		cloud.waits.checked = map[string]string{}
	}
	cloud.waits.checked[loopID] = sessionID
	answer, err := sessionStartAnswer(nil, "")
	return answer, err == nil
}

// checkSession reports whether sessionID is the session the loop's last
// check started, whose later posts are taken and dropped.
func (waits *hookWaits) checkSession(loopID, sessionID string) bool {
	waits.mu.Lock()
	defer waits.mu.Unlock()
	checked, ok := waits.checked[loopID]
	return ok && checked == sessionAddress(sessionID)
}

// SetupScript is the package's SetupScript, for a hub that holds the
// runtime and not the package.
func (cloud *Runtime) SetupScript(mcpURL string) (string, error) { return SetupScript(mcpURL) }
