package loop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// refreshFixture is a LoginRefresh over a store holding the default
// Claude login with synthetic tokens. refreshes counts the refreshes and
// rotations the rotated tokens stored; a refresh fails while fail is set,
// or for a token holding "refused".
type refreshFixture struct {
	refresh   *LoginRefresh
	store     store.Store
	refreshes int
	rotations int
	fail      bool
}

func newRefreshFixture(t *testing.T, expiresAt time.Time) *refreshFixture {
	t.Helper()
	fixture := &refreshFixture{store: openStore(t)}
	setAccount(t, fixture.store, store.DefaultClaudeLogin, store.CloudAccount{
		Token: "sk-ant-oat01-fixture-a", RefreshToken: "sk-ant-ort01-fixture-a",
		ExpiresAt: expiresAt.UnixMilli(), OrgUUID: "00000000-0000-4000-8000-000000000001",
	})
	fixture.refresh = NewLoginRefresh(fixture.store.ClaudeLogins(), func(ctx context.Context, account store.CloudAccount) (store.CloudAccount, error) {
		fixture.refreshes++
		if fixture.fail || strings.Contains(account.Token, "refused") {
			return store.CloudAccount{}, errors.New("refused")
		}
		account.Token += "-r"
		account.RefreshToken += "-r"
		account.ExpiresAt = time.Now().Add(8 * time.Hour).UnixMilli()
		return account, nil
	}, func(context.Context) { fixture.rotations++ }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return fixture
}

func (fixture *refreshFixture) token(t *testing.T, name string) string {
	t.Helper()
	account, _, err := store.SignedInAccount(context.Background(), fixture.store.ClaudeLogins(), name)
	if err != nil {
		t.Fatal(err)
	}
	return account.Token
}

func TestLoginRefreshWaitsForItsLastHour(t *testing.T) {
	fixture := newRefreshFixture(t, time.Now().Add(2*time.Hour))
	fixture.refresh.Step(context.Background(), time.Now())
	if fixture.refreshes != 0 {
		t.Fatalf("refreshed %d times with two hours left", fixture.refreshes)
	}
}

func TestLoginRefreshRotatesTheTokenInItsLastHour(t *testing.T) {
	for _, left := range []time.Duration{30 * time.Minute, -time.Minute} {
		fixture := newRefreshFixture(t, time.Now().Add(left))
		fixture.refresh.Step(context.Background(), time.Now())
		if fixture.refreshes != 1 || fixture.rotations != 1 {
			t.Fatalf("%v left: refreshed %d times and stored %d, want 1 and 1", left, fixture.refreshes, fixture.rotations)
		}
		if got := fixture.token(t, store.DefaultClaudeLogin); got != "sk-ant-oat01-fixture-a-r" {
			t.Fatalf("%v left: token after the refresh = %q, want the rotated one", left, got)
		}
	}
}

func TestAFailedLoginRefreshKeepsTheTokenAndIsRetried(t *testing.T) {
	fixture := newRefreshFixture(t, time.Now().Add(10*time.Minute))
	fixture.fail = true
	fixture.refresh.Step(context.Background(), time.Now())
	if got := fixture.token(t, store.DefaultClaudeLogin); got != "sk-ant-oat01-fixture-a" || fixture.rotations != 0 {
		t.Fatalf("after a failed refresh: token %q, %d stored; want the old one kept", got, fixture.rotations)
	}
	fixture.fail = false
	fixture.refresh.Step(context.Background(), time.Now())
	if got := fixture.token(t, store.DefaultClaudeLogin); fixture.refreshes != 2 || got != "sk-ant-oat01-fixture-a-r" {
		t.Fatalf("the retry: %d refreshes, token %q; want it rotated on the second", fixture.refreshes, got)
	}
}

func TestLoginRefreshWithNoLoginSignedInDoesNothing(t *testing.T) {
	fixture := newRefreshFixture(t, time.Now())
	setAccount(t, fixture.store, store.DefaultClaudeLogin, store.CloudAccount{})
	fixture.refresh.Step(context.Background(), time.Now())
	if fixture.refreshes != 0 {
		t.Fatalf("refreshed %d times with no login signed in", fixture.refreshes)
	}
}

// Each login is refreshed on its own: one in its last hour is refreshed
// while another with hours left is not, and one whose refresh fails
// leaves the others' refreshes to go ahead (ADR-0060).
func TestEachLoginRefreshesOnItsOwn(t *testing.T) {
	fixture := newRefreshFixture(t, time.Now().Add(2*time.Hour))
	setAccount(t, fixture.store, "ada", store.CloudAccount{
		Token: "sk-ant-oat01-fixture-ada", RefreshToken: "sk-ant-ort01-fixture-ada", ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli(),
	})
	setAccount(t, fixture.store, "grace", store.CloudAccount{
		Token: "sk-ant-oat01-fixture-refused", RefreshToken: "sk-ant-ort01-fixture-grace", ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli(),
	})
	fixture.refresh.Step(context.Background(), time.Now())
	if fixture.refreshes != 2 || fixture.rotations != 1 {
		t.Fatalf("refreshed %d times and stored %d, want 2 and 1: ada's and grace's tried, ada's stored", fixture.refreshes, fixture.rotations)
	}
	for name, want := range map[string]string{
		store.DefaultClaudeLogin: "sk-ant-oat01-fixture-a",
		"ada":                    "sk-ant-oat01-fixture-ada-r",
		"grace":                  "sk-ant-oat01-fixture-refused",
	} {
		if got := fixture.token(t, name); got != want {
			t.Errorf("%s's token after the step = %q, want %q", name, got, want)
		}
	}
}
