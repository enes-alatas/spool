package main

import (
	"bufio"
	"context"
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
	"github.com/enes-alatas/spool/internal/httpapi"
	"github.com/enes-alatas/spool/internal/roomtext"
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

// claudeSignIn runs claude auth login for the account cloud loops spend
// (ADR-0056), in a config directory of its own under dir, so it never
// reads or touches the operator's own login. The claude is the host's,
// found on PATH or at --claude-bin, or else the workstation image's, on a
// hub that has the image (#952). One runs at a time.
type claudeSignIn struct {
	bin   string
	dir   string
	image claudeImage

	mu  sync.Mutex
	run *signInRun
}

// claudeImage runs the hub's claude auth in the workstation image
// (docker.Runtime).
type claudeImage interface {
	ImageReady(ctx context.Context) error
	ClaudeAuth(ctx context.Context, configDir string, lifetime time.Duration, args []string) (*exec.Cmd, func(), error)
}

// newClaudeSignIn is the sign-in over dir, with whatever a hub stopped
// mid-sign-in left there removed: the CLI writes both tokens unsealed
// before the hub reads them, and a data directory is backed up without
// its key (ADR-0046). image is nil on a hub that runs only the host's.
func newClaudeSignIn(bin, dir string, image claudeImage) (*claudeSignIn, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	return &claudeSignIn{bin: bin, dir: dir, image: image}, nil
}

// imageReadyWait bounds Available's ask of the docker daemon.
const imageReadyWait = 5 * time.Second

// Available reports whether a claude can run the sign-in: the host's, the
// binary named or found on PATH, or the image's. It looks again each time,
// so a CLI installed or an image fetched while the hub runs is found
// without a restart.
func (signIn *claudeSignIn) Available() bool {
	if _, err := exec.LookPath(signIn.bin); err == nil {
		return true
	}
	if signIn.image == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), imageReadyWait)
	defer cancel()
	return signIn.image.ImageReady(ctx) == nil
}

// command is claude with args in configDir, ended by ctx: the host's when
// the hub finds it, or else the image's, which ends itself after lifetime.
// The caller runs remove once the command has ended, for the container
// the image's leaves.
func (signIn *claudeSignIn) command(ctx context.Context, configDir string, lifetime time.Duration, args []string) (cmd *exec.Cmd, remove func(), err error) {
	if _, lookErr := exec.LookPath(signIn.bin); lookErr == nil || signIn.image == nil {
		cmd := exec.CommandContext(ctx, signIn.bin, args...)
		cmd.Dir = configDir
		cmd.Env = append(signInEnv(os.Environ()), "CLAUDE_CONFIG_DIR="+configDir)
		cmd.SysProcAttr = runtime.ChildAttr()
		return cmd, func() {}, nil
	}
	return signIn.image.ClaudeAuth(ctx, configDir, lifetime, args)
}

// signInRun is one claude auth login, waiting for its code.
type signInRun struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// configDir is the login's CLAUDE_CONFIG_DIR, removed when it ends.
	configDir string
	// remove removes what the run leaves once it has exited: the image's
	// claude leaves its container.
	remove func()
	// exited is closed once the process has exited, and err is then why.
	exited chan struct{}
	err    error
	output *tail
	timer  *time.Timer
}

// Start ends any sign-in left unfinished, begins a new one, and returns
// the link it printed.
func (signIn *claudeSignIn) Start(ctx context.Context) (string, error) {
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
	// the image's claude outlives the wait for the code and the wait for it
	// to finish; the run is ended well before either
	cmd, remove, err := signIn.command(context.Background(), configDir, signInLifetime+signInFinishWait+time.Minute, claude.SignInArgs())
	if err != nil {
		_ = os.RemoveAll(configDir)
		return "", err
	}
	run := &signInRun{configDir: configDir, remove: remove, exited: make(chan struct{}), output: &tail{}}
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
		remove()
		_ = os.RemoveAll(configDir)
		return "", fmt.Errorf("claude auth login: start %s: %w", cmd.Path, err)
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
func (signIn *claudeSignIn) Finish(ctx context.Context, code string) (store.CloudAccount, error) {
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
		Email:        signedIn.Email,
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
func (signIn *claudeSignIn) Refresh(ctx context.Context, account store.CloudAccount) (store.CloudAccount, error) {
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
	cmd, remove, err := signIn.command(ctx, configDir, refreshWait, claude.RefreshArgs())
	if err != nil {
		return store.CloudAccount{}, err
	}
	defer remove()
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
		// a refresh signs in to the same account, whose address the CLI
		// was not given
		Email:  account.Email,
		Scopes: refreshed.Scopes,
	}
	if refreshedAccount.RefreshToken == "" {
		// a refresh that hands back no new refresh token keeps the old one
		refreshedAccount.RefreshToken = account.RefreshToken
	}
	return refreshedAccount, nil
}

// signOutWait bounds a sign-out: the CLI's logout and the one revoke it
// posts.
const signOutWait = time.Minute

// SignOut runs the CLI's own logout on account in a config directory of
// its own, which revokes the refresh token at the source (ADR-0058). The
// directory goes with the call. The CLI exits 0 whether or not the revoke
// was accepted, so a nil error says the logout ran, not that the token is
// dead. SignOut shares the sign-in's lock, as Refresh does.
func (signIn *claudeSignIn) SignOut(ctx context.Context, account store.CloudAccount) error {
	signIn.mu.Lock()
	defer signIn.mu.Unlock()
	if err := os.MkdirAll(signIn.dir, 0o700); err != nil {
		return err
	}
	configDir, err := os.MkdirTemp(signIn.dir, "sign-out-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(configDir) }()
	if err := claude.WriteSignIn(configDir, claude.SignIn{
		AccessToken:  account.Token,
		RefreshToken: account.RefreshToken,
		ExpiresAt:    account.ExpiresAt,
		Scopes:       account.Scopes,
		OrgUUID:      account.OrgUUID,
	}, false); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, signOutWait)
	defer cancel()
	cmd, remove, err := signIn.command(ctx, configDir, signOutWait, claude.SignOutArgs())
	if err != nil {
		return err
	}
	defer remove()
	output := &tail{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("claude auth logout (%v): %s", err, output.last())
	}
	// the logout deletes the credentials once it has posted the revoke
	if _, err := os.Stat(filepath.Join(configDir, ".credentials.json")); err == nil {
		return fmt.Errorf("claude auth logout left the login in place: %s", output.last())
	}
	return nil
}

// end ends run if it is still the sign-in waiting.
func (signIn *claudeSignIn) end(run *signInRun) {
	signIn.mu.Lock()
	defer signIn.mu.Unlock()
	if signIn.run == run {
		signIn.endLocked()
	}
}

// endLocked stops the waiting sign-in, if any, and removes its config
// directory, tokens and all. The caller holds mu.
func (signIn *claudeSignIn) endLocked() {
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
	run.remove()
	_ = os.RemoveAll(run.configDir)
}

// signInEnv is the hub's environment without what would sign the CLI in
// as something else: a token in the environment outranks a login. It sets
// CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC, as a contained loop's claude
// has it, so the CLI's runs send no telemetry from the hub's machine; the
// sign-in, the refresh and the logout still make their calls with it set
// (FIDELITY.md, #935).
func signInEnv(environ []string) []string {
	var env []string
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
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
// nothing did. The line is the CLI's words and reaches the room in a
// sign-in refusal, so it comes back without backticks (#959).
func (output *tail) last() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(output.buf)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		// the prompt the CLI repeats is not the reason
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "Paste code here if prompted >"))
		if line != "" {
			return roomtext.Plain(line)
		}
	}
	return "the CLI said nothing"
}
