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
	if err := db.Settings().Set(context.Background(), store.SettingCloudAccount, `{"token":"synthetic"}`); err != nil {
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
// not finish over Remove's: once Remove returns, the last check says there
// is no account (#970).
func TestACheckBegunBeforeRemoveDoesNotOutliveIt(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Settings().Set(context.Background(), store.SettingCloudAccount, `{"token":"synthetic"}`); err != nil {
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

	server.changeCloudAccount(httptest.NewRecorder(), httptest.NewRequest("DELETE", "/api/settings/cloud/account", nil), "")
	close(checker.release)
	<-booted
	if check := server.cloudAccountCheck.result(); check == nil || check.Stage != CloudAccountStageNone {
		t.Fatalf("after Remove and the earlier check: %+v, want no account", check)
	}
}
