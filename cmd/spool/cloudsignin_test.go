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
	if _, err := signIn.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-signIn.run.exited
	if _, err := signIn.Finish(context.Background(), "fixture-code"); !errors.Is(err, httpapi.ErrNoCloudSignIn) {
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
	if _, err := signIn.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(image.runs) != 1 || !slices.Equal(image.runs[0], []string{"auth", "login", "--claudeai"}) {
		t.Fatalf("the image ran %v, want one claude auth login", image.runs)
	}
	if _, err := signIn.Finish(context.Background(), "fixture-code"); !errors.Is(err, httpapi.ErrCloudSignInRefused) {
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
	if _, err := signIn.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	signIn.end(signIn.run)
	if len(image.runs) != 0 {
		t.Errorf("a hub with its own claude ran the image's: %v", image.runs)
	}
}
