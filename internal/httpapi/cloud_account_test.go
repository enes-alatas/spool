package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// deadlineChecker passes the check, and keeps the deadline it was given.
type deadlineChecker struct{ deadline time.Time }

func (checker *deadlineChecker) CheckAccount(ctx context.Context) (string, string) {
	checker.deadline, _ = ctx.Deadline()
	return "", ""
}

// The account check has a deadline of its own, since boot's and a sign-in's
// contexts have none: a CLI that stalls can't hold it open.
func TestTheCloudAccountCheckHasATimeLimit(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ClaudeLogins().Create(context.Background(), &store.ClaudeLogin{Name: store.DefaultClaudeLogin, Account: store.CloudAccount{Token: "synthetic"}, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	checker := &deadlineChecker{}
	server := &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CloudAccounts: checker}
	server.CheckCloudAccount(context.Background())
	if left := time.Until(checker.deadline); left <= 0 || left > cloudAccountCheckTimeout {
		t.Errorf("the check's deadline is %v away, want within %v", left, cloudAccountCheckTimeout)
	}
}

// heldChecker passes the check once release is closed, and says on started
// that it began.
type heldChecker struct{ started, release chan struct{} }

func (checker *heldChecker) CheckAccount(context.Context) (string, string) {
	close(checker.started)
	<-checker.release
	return "", ""
}

// A check that began while the account was signed in, as boot's does, must
// not finish over a sign-out's: once the sign-out returns, the last check
// says there is no account (#970).
func TestACheckBegunBeforeRemoveDoesNotOutliveIt(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ClaudeLogins().Create(context.Background(), &store.ClaudeLogin{Name: store.DefaultClaudeLogin, Account: store.CloudAccount{Token: "synthetic"}, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	checker := &heldChecker{started: make(chan struct{}), release: make(chan struct{})}
	server := &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CloudAccounts: checker}
	booted := make(chan struct{})
	go func() {
		defer close(booted)
		server.CheckCloudAccount(context.Background())
	}()
	<-checker.started

	server.changeLogin(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/claude-logins/default/sign-out", nil), store.DefaultClaudeLogin, store.CloudAccount{})
	close(checker.release)
	<-booted
	if check := server.cloudAccountCheck.result(); check == nil || check.Stage != CloudAccountStageNone {
		t.Fatalf("after the sign-out and the earlier check: %+v, want no account", check)
	}
}

// A sign-in as another account fails a passed check that ran under the
// old one, and revokes the loop's hub token only where both accounts' IDs
// are known: a check run before the hub read the ID, as every check is
// right after an upgrade, fails once and keeps its token, so the operator
// has no secret to paste again (#1230).
func TestASignInAsAnotherAccountRevokesOnlyAKnownAccountsToken(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	checks := map[string]*store.CloudCheck{
		"unknown":       {State: store.CloudCheckOK, Account: ""},
		"old":           {State: store.CloudCheckOK, Account: "00000000-0000-4000-8000-0000000000a1"},
		"old-failed":    {State: store.CloudCheckFailed, Stage: cloudStepEnvironment, Account: "00000000-0000-4000-8000-0000000000a1"},
		"same":          {State: store.CloudCheckOK, Account: "00000000-0000-4000-8000-0000000000b2"},
		"unknown-ended": {State: store.CloudCheckFailed, Stage: cloudStepEnvironment, Account: ""},
	}
	for name, check := range checks {
		loopRecord := store.Loop{ID: "l-" + name, Name: name, Runtime: store.RuntimeCloud, ClaudeLogin: "ada",
			Status: store.StatusActive, WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, CreatedAt: 1, UpdatedAt: 1}
		if err := db.Loops().Create(ctx, &loopRecord); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Loops().RotateHubMCPToken(ctx, loopRecord.ID); err != nil {
			t.Fatal(err)
		}
		if err := db.Loops().SetCloudCheck(ctx, loopRecord.ID, check); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	server.failChecksOfAnotherAccount(ctx, "ada", "00000000-0000-4000-8000-0000000000b2")

	for name, want := range map[string]struct {
		state    string
		hasToken bool
	}{
		"unknown":       {store.CloudCheckFailed, true},
		"old":           {store.CloudCheckFailed, false},
		"old-failed":    {store.CloudCheckFailed, false},
		"same":          {store.CloudCheckOK, true},
		"unknown-ended": {store.CloudCheckFailed, true},
	} {
		loopRecord, err := db.Loops().Get(ctx, "l-"+name)
		if err != nil {
			t.Fatal(err)
		}
		if loopRecord.CloudCheck.State != want.state || loopRecord.HasHubToken != want.hasToken {
			t.Errorf("%s: check %s, hub token %v; want %s, %v", name, loopRecord.CloudCheck.State, loopRecord.HasHubToken, want.state, want.hasToken)
		}
	}
}
