package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// revokeRecorder is a sign-in source that keeps every account it was asked
// to sign out, and every login whose waiting sign-in it was asked to end.
type revokeRecorder struct {
	revoked []store.CloudAccount
	ended   []string
}

func (recorder *revokeRecorder) Start(context.Context, string, string) (string, error) {
	return "", nil
}
func (recorder *revokeRecorder) Finish(context.Context, string, string) (store.CloudAccount, error) {
	return store.CloudAccount{}, nil
}
func (recorder *revokeRecorder) Available() bool  { return true }
func (recorder *revokeRecorder) End(login string) { recorder.ended = append(recorder.ended, login) }
func (recorder *revokeRecorder) SignOut(_ context.Context, account store.CloudAccount) error {
	recorder.revoked = append(recorder.revoked, account)
	return nil
}

// A sign-in that finishes after an admin removed its login, which the CLI
// can take its time over, doesn't bring the login back without its owner:
// it is refused and revoked at the source.
func TestASignInFinishedAfterItsLoginWasRemovedIsRefused(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: "work", OwnerUserID: "u1", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	recorder := &revokeRecorder{}
	server := &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CloudSignIn: recorder}
	if err := db.ClaudeLogins().Delete(ctx, "work"); err != nil {
		t.Fatal(err)
	}

	account := store.CloudAccount{Token: "synthetic-access", RefreshToken: "synthetic-refresh"}
	response := httptest.NewRecorder()
	server.changeLogin(response, httptest.NewRequest("POST", "/api/claude-logins/work/sign-in/code", nil), "work", account)
	if response.Code != 409 {
		t.Errorf("finishing the removed login's sign-in: %d %s, want 409", response.Code, response.Body)
	}
	if _, err := db.ClaudeLogins().Get(ctx, "work"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the removed login after its sign-in finished: err %v, want not found", err)
	}
	if len(recorder.revoked) != 1 || recorder.revoked[0].RefreshToken != account.RefreshToken {
		t.Errorf("revoked at the source: %+v, want the stray sign-in", recorder.revoked)
	}
}

// A request that reached a login's route with no caller, as one the guard
// missed would, is refused rather than let through.
func TestALoginRouteWithNoCallerIsRefused(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	server := &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest("POST", "/api/claude-logins/default/sign-out", nil)
	request.SetPathValue("name", store.DefaultClaudeLogin)
	response := httptest.NewRecorder()
	if login := server.managedLogin(response, request); login != nil || response.Code != 403 {
		t.Errorf("no caller: login %+v, %d, want refused with 403", login, response.Code)
	}
}

// Removing a login ends its sign-in waiting for a code, which would
// otherwise hold its starter's slot under the caps for its lifetime.
func TestRemovingALoginEndsItsWaitingSignIn(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: "work", OwnerUserID: "u1", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	recorder := &revokeRecorder{}
	server := &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CloudSignIn: recorder}
	request := httptest.NewRequest("DELETE", "/api/claude-logins/work", nil)
	request.SetPathValue("name", "work")
	request = request.WithContext(context.WithValue(request.Context(), callerKey{}, &caller{}))
	response := httptest.NewRecorder()
	server.handleRemoveClaudeLogin(response, request)
	if response.Code != 204 {
		t.Fatalf("removing work: %d %s, want 204", response.Code, response.Body)
	}
	if len(recorder.ended) != 1 || recorder.ended[0] != "work" {
		t.Errorf("sign-ins ended: %v, want work's", recorder.ended)
	}
}
