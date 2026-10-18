package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/httpapi"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// signInLifetime is how long a sign-in may wait for its code before it is
// ended: long enough to open the link and sign in, short enough that an
// abandoned one doesn't stay.
const signInLifetime = 10 * time.Minute

// signInLinkWait bounds the wait for the CLI to print its link, and
// signInFinishWait the wait for it to finish once it has the code.
const (
	signInLinkWait   = 30 * time.Second
	signInFinishWait = 30 * time.Second
)

// hostSignIn runs the host's claude auth login for the account cloud loops
// spend (ADR-0056), in a config directory of its own under dir, so it
// never reads or touches the operator's own login. One runs at a time.
type hostSignIn struct {
	bin string
	dir string

	mu  sync.Mutex
	run *signInRun
}

// newHostSignIn is the sign-in over dir, with whatever a hub stopped
// mid-sign-in left there removed: the CLI writes both tokens unsealed
// before the hub reads them, and a data directory is backed up without
// its key (ADR-0046).
func newHostSignIn(bin, dir string) (*hostSignIn, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	return &hostSignIn{bin: bin, dir: dir}, nil
}

// signInRun is one claude auth login, waiting for its code.
type signInRun struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// configDir is the login's CLAUDE_CONFIG_DIR, removed when it ends.
	configDir string
	// exited is closed once the process has exited, and err is then why.
	exited chan struct{}
	err    error
	output *tail
	timer  *time.Timer
}

// Start ends any sign-in left unfinished, begins a new one, and returns
// the link it printed.
func (signIn *hostSignIn) Start(ctx context.Context) (string, error) {
	signIn.mu.Lock()
	defer signIn.mu.Unlock()
	signIn.endLocked()
	// with none waiting, anything in dir is a sign-in no one ended
	if err := os.RemoveAll(signIn.dir); err != nil {
		return "", err
	}
	if err := os.Mkdir(signIn.dir, 0o700); err != nil {
		return "", err
	}
	configDir, err := os.MkdirTemp(signIn.dir, "login-")
	if err != nil {
		return "", err
	}
	run := &signInRun{configDir: configDir, exited: make(chan struct{}), output: &tail{}}
	cmd := exec.Command(signIn.bin, claude.SignInArgs()...)
	cmd.Dir = configDir
	cmd.Env = append(signInEnv(os.Environ()), "CLAUDE_CONFIG_DIR="+configDir)
	cmd.SysProcAttr = runtime.ChildAttr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(configDir)
		return "", err
	}
	read, write, err := os.Pipe()
	if err != nil {
		_ = os.RemoveAll(configDir)
		return "", err
	}
	cmd.Stdout, cmd.Stderr = write, write
	if err := cmd.Start(); err != nil {
		_ = read.Close()
		_ = write.Close()
		_ = os.RemoveAll(configDir)
		return "", fmt.Errorf("claude auth login: start %s: %w", signIn.bin, err)
	}
	_ = write.Close()
	run.cmd, run.stdin = cmd, stdin
	go func() {
		run.err = cmd.Wait()
		close(run.exited)
	}()

	// The link comes first; whatever the CLI says after it is kept, for a
	// sign-in that fails to say why.
	output := bufio.NewReader(read)
	link := make(chan string, 1)
	failed := make(chan error, 1)
	go func() {
		found, err := claude.SignInLink(output)
		if err != nil {
			failed <- err
			_ = read.Close()
			return
		}
		link <- found
		_, _ = io.Copy(run.output, output)
		_ = read.Close()
	}()
	signIn.run = run
	select {
	case found := <-link:
		run.timer = time.AfterFunc(signInLifetime, func() { signIn.end(run) })
		return found, nil
	case err := <-failed:
		signIn.endLocked()
		return "", err
	case <-time.After(signInLinkWait):
		signIn.endLocked()
		return "", errors.New("claude auth login printed no link in time")
	case <-ctx.Done():
		signIn.endLocked()
		return "", ctx.Err()
	}
}

// Finish hands the waiting sign-in the code the page showed, and returns
// what the CLI signed in as. The sign-in ends either way: one whose code
// was refused starts again.
func (signIn *hostSignIn) Finish(ctx context.Context, code string) (store.CloudAccount, error) {
	signIn.mu.Lock()
	defer signIn.mu.Unlock()
	run := signIn.run
	if run == nil {
		return store.CloudAccount{}, httpapi.ErrNoCloudSignIn
	}
	defer signIn.endLocked()
	// a CLI that has ended on its own takes no code: the operator starts again
	select {
	case <-run.exited:
		return store.CloudAccount{}, fmt.Errorf("%w: claude auth login ended: %s", httpapi.ErrNoCloudSignIn, run.output.last())
	default:
	}
	if _, err := io.WriteString(run.stdin, strings.TrimSpace(code)+"\n"); err != nil {
		return store.CloudAccount{}, fmt.Errorf("claude auth login: %w", err)
	}
	select {
	case <-run.exited:
	case <-time.After(signInFinishWait):
		return store.CloudAccount{}, fmt.Errorf("%w: claude auth login didn't finish: %s", httpapi.ErrCloudSignInRefused, run.output.last())
	case <-ctx.Done():
		return store.CloudAccount{}, ctx.Err()
	}
	if run.err != nil {
		return store.CloudAccount{}, fmt.Errorf("%w: %s", httpapi.ErrCloudSignInRefused, run.output.last())
	}
	signedIn, err := claude.ReadSignIn(run.configDir)
	if err != nil {
		return store.CloudAccount{}, err
	}
	return store.CloudAccount{
		Token:        signedIn.AccessToken,
		RefreshToken: signedIn.RefreshToken,
		ExpiresAt:    signedIn.ExpiresAt,
		OrgUUID:      signedIn.OrgUUID,
		Scopes:       signedIn.Scopes,
	}, nil
}

// refreshWait bounds a refresh: the CLI's status read and the one call it
// makes to the token endpoint.
const refreshWait = time.Minute

// Refresh hands account to the host's claude in a config directory of its
// own, written as already expired so that the CLI refreshes it now, and
// returns the rotated sign-in the CLI wrote back (ADR-0058). The directory
// goes with the call, tokens and all. Refresh shares the sign-in's lock,
// so it never runs beside a sign-in's start or finish, whose start empties
// the directory both work in.
func (signIn *hostSignIn) Refresh(ctx context.Context, account store.CloudAccount) (store.CloudAccount, error) {
	signIn.mu.Lock()
	defer signIn.mu.Unlock()
	if err := os.MkdirAll(signIn.dir, 0o700); err != nil {
		return store.CloudAccount{}, err
	}
	configDir, err := os.MkdirTemp(signIn.dir, "refresh-")
	if err != nil {
		return store.CloudAccount{}, err
	}
	defer func() { _ = os.RemoveAll(configDir) }()
	if err := claude.WriteSignIn(configDir, claude.SignIn{
		AccessToken:  account.Token,
		RefreshToken: account.RefreshToken,
		ExpiresAt:    account.ExpiresAt,
		Scopes:       account.Scopes,
		OrgUUID:      account.OrgUUID,
	}, true); err != nil {
		return store.CloudAccount{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, refreshWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, signIn.bin, claude.RefreshArgs()...)
	cmd.Dir = configDir
	cmd.Env = append(signInEnv(os.Environ()), "CLAUDE_CONFIG_DIR="+configDir)
	cmd.SysProcAttr = runtime.ChildAttr()
	output := &tail{}
	cmd.Stdout, cmd.Stderr = output, output
	runErr := cmd.Run()
	refreshed, err := claude.ReadSignIn(configDir)
	switch {
	case err != nil:
		return store.CloudAccount{}, err
	case refreshed.AccessToken == account.Token:
		// the CLI kept the token it was given, whatever it said
		if runErr != nil {
			return store.CloudAccount{}, fmt.Errorf("claude auth status didn't refresh the token (%v): %s", runErr, output.last())
		}
		return store.CloudAccount{}, fmt.Errorf("claude auth status didn't refresh the token: %s", output.last())
	}
	refreshedAccount := store.CloudAccount{
		Token:        refreshed.AccessToken,
		RefreshToken: refreshed.RefreshToken,
		ExpiresAt:    refreshed.ExpiresAt,
		OrgUUID:      refreshed.OrgUUID,
		Scopes:       refreshed.Scopes,
	}
	if refreshedAccount.RefreshToken == "" {
		// a refresh that hands back no new refresh token keeps the old one
		refreshedAccount.RefreshToken = account.RefreshToken
	}
	return refreshedAccount, nil
}

// end ends run if it is still the sign-in waiting.
func (signIn *hostSignIn) end(run *signInRun) {
	signIn.mu.Lock()
	defer signIn.mu.Unlock()
	if signIn.run == run {
		signIn.endLocked()
	}
}

// endLocked stops the waiting sign-in, if any, and removes its config
// directory, tokens and all. The caller holds mu.
func (signIn *hostSignIn) endLocked() {
	run := signIn.run
	if run == nil {
		return
	}
	signIn.run = nil
	if run.timer != nil {
		run.timer.Stop()
	}
	_ = run.stdin.Close()
	_ = run.cmd.Process.Kill()
	<-run.exited
	_ = os.RemoveAll(run.configDir)
}

// signInEnv is the hub's environment without what would sign the CLI in
// as something else: a token in the environment outranks a login.
func signInEnv(environ []string) []string {
	var env []string
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN":
			continue
		}
		env = append(env, entry)
	}
	return env
}

// tail keeps the last few lines a process wrote, for an error to quote.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

const tailBytes = 2 << 10

func (output *tail) Write(p []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.buf = append(output.buf, p...)
	if len(output.buf) > tailBytes {
		output.buf = output.buf[len(output.buf)-tailBytes:]
	}
	return len(p), nil
}

// last is the last line written that says something, or a note that
// nothing did.
func (output *tail) last() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(output.buf)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		// the prompt the CLI repeats is not the reason
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "Paste code here if prompted >"))
		if line != "" {
			return line
		}
	}
	return "the CLI said nothing"
}
