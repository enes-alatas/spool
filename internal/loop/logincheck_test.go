package loop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// checkingRuntime is a bare runtime whose login checks answer, in turn,
// what the test sends them; nothing else of the seam is used here.
type checkingRuntime struct {
	runtime.Runtime
	answers chan claude.LoginCheck
}

func (checkingRuntime) Kind() string { return store.RuntimeBare }

func (rt checkingRuntime) CheckLogin(ctx context.Context, _ string) (claude.LoginCheck, error) {
	select {
	case answer := <-rt.answers:
		return answer, nil
	case <-ctx.Done():
		return claude.LoginCheck{}, ctx.Err()
	}
}

// TestALoginCheckKeepsOnlyTheNewestOutcome: Start stores a pending record
// at once, and a check started while another runs supersedes it, so the
// older one's late answer is dropped whichever way it went (ADR-0044).
func TestALoginCheckKeepsOnlyTheNewestOutcome(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	rt := checkingRuntime{answers: make(chan claude.LoginCheck)}
	checker := NewLoginChecker(db, rt, slog.New(slog.NewTextHandler(io.Discard, nil)))
	last := func() store.LoginCheckRecord {
		t.Helper()
		record, err := checker.Last(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}

	if got := last(); got.Status != "" {
		t.Fatalf("before any check: %+v, want the zero record", got)
	}
	if err := checker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if got := last(); got.Status != store.LoginCheckPending {
		t.Fatalf("right after Start: %+v, want pending", got)
	}
	first := last().At
	if err := checker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	second := last().At
	if second <= first {
		t.Fatalf("the second check began at %d, not after the first's %d", second, first)
	}

	// Two checks are running; whichever takes an answer first, the
	// refusal is sent before the acceptance.
	rt.answers <- claude.LoginCheck{Refusal: "Failed to authenticate"}
	rt.answers <- claude.LoginCheck{OK: true}
	for deadline := time.Now().Add(5 * time.Second); last().Status == store.LoginCheckPending; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the newest check never finished")
		}
	}
	time.Sleep(50 * time.Millisecond) // let the other answer land, if it would
	if got := last(); got.At != second {
		t.Fatalf("kept %+v, want the second check's outcome (began %d)", got, second)
	}

	if err := checker.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if got := last(); got.Status != "" {
		t.Fatalf("after Clear: %+v, want the zero record", got)
	}
}

// TestALoginCheckLeftPendingReadsInconclusive: a hub stopped mid-check
// leaves a pending record no run will finish, so once the check's bound has
// passed it reads as one that did not finish, not as one still running.
func TestALoginCheckLeftPendingReadsInconclusive(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{time.Second, store.LoginCheckPending},
		{loginCheckTimeout + time.Second, store.LoginCheckInconclusive},
	} {
		at := time.Now().Add(-tc.age).UnixMilli()
		raw, _ := json.Marshal(store.LoginCheckRecord{Status: store.LoginCheckPending, At: at})
		if err := db.Settings().Set(ctx, store.SettingLoginCheck, string(raw)); err != nil {
			t.Fatal(err)
		}
		got, err := LastLoginCheck(ctx, db.Settings())
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tc.want || got.At != at {
			t.Errorf("a check pending for %v reads %+v, want %s at %d", tc.age, got, tc.want, at)
		}
	}
}

func openStore(t *testing.T) store.Store {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// setAccount stores account as the sign-in of the login called name,
// adding the login if the store doesn't hold it.
func setAccount(t *testing.T, st store.Store, name string, account store.CloudAccount) {
	t.Helper()
	ctx := context.Background()
	err := st.ClaudeLogins().SetAccount(ctx, name, account)
	if errors.Is(err, store.ErrNotFound) {
		err = st.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: name, Account: account, CreatedAt: 1})
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestHubClaudeTokenPrefersTheDefaultLogin(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	if token, err := HubClaudeToken(ctx, st); err != nil || token != "" {
		t.Fatalf("with nothing stored: %q, %v", token, err)
	}
	if err := st.Settings().Set(ctx, store.SettingClaudeOAuthToken, "sk-ant-oat01-fixture-setup"); err != nil {
		t.Fatal(err)
	}
	if token, _ := HubClaudeToken(ctx, st); token != "sk-ant-oat01-fixture-setup" {
		t.Fatalf("with a setup-token only: %q", token)
	}
	setAccount(t, st, "ada", store.CloudAccount{Token: "sk-ant-oat01-fixture-ada", OrgUUID: "org"})
	if token, _ := HubClaudeToken(ctx, st); token != "sk-ant-oat01-fixture-setup" {
		t.Fatalf("with another login only: %q, want the setup-token", token)
	}
	setAccount(t, st, store.DefaultClaudeLogin, store.CloudAccount{Token: "sk-ant-oat01-fixture-login", OrgUUID: "org"})
	if token, _ := HubClaudeToken(ctx, st); token != "sk-ant-oat01-fixture-login" {
		t.Fatalf("with both: %q, want the default login's", token)
	}
	setAccount(t, st, store.DefaultClaudeLogin, store.CloudAccount{})
	if token, _ := HubClaudeToken(ctx, st); token != "sk-ant-oat01-fixture-setup" {
		t.Fatalf("with the default login signed out: %q, want the setup-token", token)
	}
}

// A login other than the default is checked on the contained runtime and
// keeps its outcome on itself, leaving the hub's check alone; with no
// contained runtime, or no sign-in, its check ends inconclusive (ADR-0060).
func TestALoginsCheckIsKeptOnTheLogin(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	setAccount(t, st, "ada", store.CloudAccount{Token: "sk-ant-oat01-fixture-ada"})
	contained := checkingRuntime{answers: make(chan claude.LoginCheck, 1)}
	checker := NewLoginChecker(st, checkingRuntime{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	checkOf := func(name string) *store.LoginCheckRecord {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			login, err := st.ClaudeLogins().Get(ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			if login.Check != nil && login.Check.Status != store.LoginCheckPending || time.Now().After(deadline) {
				return login.Check
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	if err := checker.StartLogin(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	if got := checkOf("ada"); got == nil || got.Status != store.LoginCheckInconclusive {
		t.Fatalf("with no contained runtime: %+v, want inconclusive", got)
	}
	checker.Contained = contained
	contained.answers <- claude.LoginCheck{Refusal: "invalid"}
	if err := checker.StartLogin(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	if got := checkOf("ada"); got == nil || got.Status != store.LoginCheckRefused || got.Refusal != "invalid" {
		t.Fatalf("a refused check: %+v, want refused", got)
	}
	contained.answers <- claude.LoginCheck{OK: true}
	if err := checker.StartLogin(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	if got := checkOf("ada"); got == nil || got.Status != store.LoginCheckOK {
		t.Fatalf("a passed check: %+v, want ok", got)
	}
	if hub, err := checker.Last(ctx); err != nil || hub.Status != "" {
		t.Errorf("ada's checks touched the hub's: %+v (%v)", hub, err)
	}
	setAccount(t, st, "ada", store.CloudAccount{})
	if err := checker.StartLogin(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	if got := checkOf("ada"); got == nil || got.Status != store.LoginCheckInconclusive {
		t.Fatalf("signed out: %+v, want inconclusive", got)
	}
	if err := checker.StartLogin(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("checking a login the hub doesn't hold: %v, want ErrNotFound", err)
	}
}
