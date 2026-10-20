package httpapi

import (
	"context"
	"io"
	"log/slog"
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
