package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	signIn, err := newHostSignIn(bin, filepath.Join(dir, "cloud-sign-in"))
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
