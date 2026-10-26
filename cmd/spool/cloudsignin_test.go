package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/httpapi"
	"github.com/enes-alatas/spool/internal/store"
)

// A CLI that ended on its own, as at its own timeout, takes no code: the
// page is told to start again, not shown the broken pipe.
func TestACodeForAnEndedSignInAsksForANewOne(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho 'visit: https://claude.ai/oauth/authorize?code=true'\necho 'timed out'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	signIn, err := newClaudeSignIn(bin, filepath.Join(dir, "cloud-sign-in"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signIn.Start(context.Background(), store.DefaultClaudeLogin, ""); err != nil {
		t.Fatal(err)
	}
	<-signIn.runs[store.DefaultClaudeLogin].exited
	if _, err := signIn.Finish(context.Background(), store.DefaultClaudeLogin, "fixture-code"); !errors.Is(err, httpapi.ErrNoCloudSignIn) {
		t.Fatalf("Finish after the CLI ended = %v, want ErrNoCloudSignIn", err)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "cloud-sign-in")); err != nil || len(entries) != 0 {
		t.Errorf("the ended sign-in left %v (%v), want nothing", entries, err)
	}
}

// The sign-in finds its CLI each time it is asked, so a CLI installed
// while the hub runs is found without a restart (ADR-0058).
func TestTheSignInFindsACLIInstalledWhileTheHubRuns(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	signIn, err := newClaudeSignIn(bin, filepath.Join(dir, "cloud-sign-in"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if signIn.Available() {
		t.Fatal("a sign-in with no CLI at its path reports it available")
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !signIn.Available() {
		t.Fatal("a CLI installed after the hub started was not found")
	}
}

// The CLI's runs leave out what would sign it in as something else, and
// send no nonessential traffic whatever the hub's environment says (#935).
func TestTheSignInEnvironmentIsQuietAndCarriesNoOtherCredential(t *testing.T) {
	env := signInEnv([]string{
		"HOME=/home/fixture",
		"CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-fixture",
		"ANTHROPIC_API_KEY=fixture",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=0",
	})
	want := []string{"HOME=/home/fixture", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	if !slices.Equal(env, want) {
		t.Fatalf("signInEnv = %q, want %q", env, want)
	}
}

// fakeImage stands in for the workstation image's claude: it runs script
// in place of the container, and counts what it was asked.
type fakeImage struct {
	script  string
	ready   error
	runs    [][]string
	removed int
}

func (image *fakeImage) ImageReady(context.Context) error { return image.ready }

func (image *fakeImage) ClaudeAuth(ctx context.Context, configDir string, _ time.Duration, args []string) (*exec.Cmd, func(), error) {
	image.runs = append(image.runs, args)
	cmd := exec.CommandContext(ctx, image.script, args...)
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir)
	return cmd, func() { image.removed++ }, nil
}

// A hub with no claude of its own signs in with the image's, and its
// container is removed once the sign-in ends (#952). With the image
// missing too, no sign-in can run, and the page says so.
func TestASignInWithNoHostCLIRunsInTheImage(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "image-claude")
	login := "#!/bin/sh\necho 'visit: https://claude.ai/oauth/authorize?code=true'\nread code\necho 'Invalid code.'\nexit 1\n"
	if err := os.WriteFile(script, []byte(login), 0o700); err != nil {
		t.Fatal(err)
	}
	image := &fakeImage{script: script, ready: errors.New("no such image")}
	signIn, err := newClaudeSignIn(filepath.Join(dir, "no-claude"), filepath.Join(dir, "cloud-sign-in"), image)
	if err != nil {
		t.Fatal(err)
	}
	if signIn.Available() {
		t.Fatal("a hub with no host CLI and no image reports a sign-in can run")
	}
	image.ready = nil
	if !signIn.Available() {
		t.Fatal("a hub with the image reports no sign-in can run")
	}
	if _, err := signIn.Start(context.Background(), store.DefaultClaudeLogin, ""); err != nil {
		t.Fatal(err)
	}
	if len(image.runs) != 1 || !slices.Equal(image.runs[0], []string{"auth", "login", "--claudeai"}) {
		t.Fatalf("the image ran %v, want one claude auth login", image.runs)
	}
	if _, err := signIn.Finish(context.Background(), store.DefaultClaudeLogin, "fixture-code"); !errors.Is(err, httpapi.ErrCloudSignInRefused) {
		t.Fatalf("Finish on a refused code = %v, want ErrCloudSignInRefused", err)
	}
	if image.removed != 1 {
		t.Errorf("the image's container was removed %d times, want once", image.removed)
	}
}

// A hub with its own claude runs that one, image or not.
func TestASignInPrefersTheHostCLI(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'visit: https://claude.ai/oauth/authorize?code=true'\nread code\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	image := &fakeImage{script: bin}
	signIn, err := newClaudeSignIn(bin, filepath.Join(dir, "cloud-sign-in"), image)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signIn.Start(context.Background(), store.DefaultClaudeLogin, ""); err != nil {
		t.Fatal(err)
	}
	signIn.end(store.DefaultClaudeLogin, signIn.runs[store.DefaultClaudeLogin])
	if len(image.runs) != 0 {
		t.Errorf("a hub with its own claude ran the image's: %v", image.runs)
	}
}

// Each login's sign-in waits on its own: starting one for another login
// leaves the first waiting for its code, and starting one again for the
// same login ends the one it left (ADR-0060).
func TestEachLoginSignsInOnItsOwn(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'visit: https://claude.ai/oauth/authorize?code=true'\nread code\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	signIn, err := newClaudeSignIn(bin, filepath.Join(dir, "cloud-sign-in"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := signIn.Start(ctx, "ada", ""); err != nil {
		t.Fatal(err)
	}
	ada := signIn.runs["ada"]
	if _, err := signIn.Start(ctx, "grace", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ada.exited:
		t.Fatal("starting grace's sign-in ended ada's")
	default:
	}
	if _, err := signIn.Start(ctx, "ada", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ada.exited:
	default:
		t.Fatal("starting ada's sign-in again left the first waiting")
	}
	for _, login := range []string{"ada", "grace"} {
		signIn.end(login, signIn.runs[login])
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "cloud-sign-in")); err != nil || len(entries) != 0 {
		t.Errorf("the ended sign-ins left %v (%v), want nothing", entries, err)
	}
}

// The CLI's last line is its words, and a sign-in refusal carries it to the
// room: it draws no copy chip (#959).
func TestTailLastQuotesTheCLIWithoutMarks(t *testing.T) {
	var output tail
	if _, err := output.Write([]byte("Login failed: run `claude doctor`\n")); err != nil {
		t.Fatal(err)
	}
	if got, want := output.last(), "Login failed: run 'claude doctor'"; got != want {
		t.Errorf("last() = %q, want %q", got, want)
	}
}

// Sign-ins waiting for a code are capped, since each holds a CLI or a
// container on the hub's host: one per user, and maxSignIns across the
// hub. Starting a login's own again replaces it, so it passes either cap.
func TestSignInsWaitingForACodeAreCapped(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'visit: https://claude.ai/oauth/authorize?code=true'\nread code\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	signIn, err := newClaudeSignIn(bin, filepath.Join(dir, "cloud-sign-in"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		for login, run := range signIn.runs {
			signIn.end(login, run)
		}
	})
	if _, err := signIn.Start(ctx, "ada", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := signIn.Start(ctx, "ada2", "u1"); !errors.Is(err, httpapi.ErrTooManySignIns) {
		t.Fatalf("u1's second sign-in: %v, want ErrTooManySignIns", err)
	}
	if _, err := signIn.Start(ctx, "ada", "u1"); err != nil {
		t.Fatalf("u1 starting ada's again: %v, want it replaced", err)
	}
	for login, by := range map[string]string{"grace": "u2", "alan": "u3", "default": ""} {
		if _, err := signIn.Start(ctx, login, by); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := signIn.Start(ctx, "edsger", ""); !errors.Is(err, httpapi.ErrTooManySignIns) {
		t.Fatalf("a sign-in past %d waiting: %v, want ErrTooManySignIns", maxSignIns, err)
	}
	if len(signIn.runs) != maxSignIns {
		t.Errorf("%d sign-ins waiting, want %d", len(signIn.runs), maxSignIns)
	}
}

// A removed login's waiting sign-in is ended, so its starter can start
// another straight away under the one-per-user cap.
func TestEndingASignInFreesItsStartersSlot(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'visit: https://claude.ai/oauth/authorize?code=true'\nread code\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	signIn, err := newClaudeSignIn(bin, filepath.Join(dir, "cloud-sign-in"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { signIn.End("work2") })
	if _, err := signIn.Start(ctx, "work", "u1"); err != nil {
		t.Fatal(err)
	}
	signIn.End("work")
	if _, err := signIn.Start(ctx, "work2", "u1"); err != nil {
		t.Fatalf("u1's sign-in after work's was ended: %v, want it started", err)
	}
	if _, waiting := signIn.runs["work"]; waiting {
		t.Error("work's sign-in still waits after End")
	}
}
