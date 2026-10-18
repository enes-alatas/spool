package loop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// refreshFixture is a LoginRefresh over a store holding a Claude login
// with synthetic tokens. refreshes counts the refreshes and rotations the
// rotated tokens stored; fail makes the refresh fail.
type refreshFixture struct {
	refresh   *LoginRefresh
	settings  store.SettingsStore
	refreshes int
	rotations int
	fail      bool
}

func newRefreshFixture(t *testing.T, expiresAt time.Time) *refreshFixture {
	t.Helper()
	fixture := &refreshFixture{settings: openSettings(t)}
	setAccount(t, fixture.settings, store.CloudAccount{
		Token: "sk-ant-oat01-fixture-a", RefreshToken: "sk-ant-ort01-fixture-a",
		ExpiresAt: expiresAt.UnixMilli(), OrgUUID: "00000000-0000-4000-8000-000000000001",
	})
	fixture.refresh = NewLoginRefresh(fixture.settings, func(ctx context.Context, account store.CloudAccount) (store.CloudAccount, error) {
		fixture.refreshes++
		if fixture.fail {
			return store.CloudAccount{}, errors.New("refused")
		}
		account.Token += "-r"
		account.RefreshToken += "-r"
		account.ExpiresAt = time.Now().Add(8 * time.Hour).UnixMilli()
		return account, nil
	}, func(context.Context) { fixture.rotations++ }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return fixture
}

func (fixture *refreshFixture) token(t *testing.T) string {
	t.Helper()
	token, err := HubClaudeToken(context.Background(), fixture.settings)
	if err != nil {
		t.Fatal(err)
	}
	return token
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
		if got := fixture.token(t); got != "sk-ant-oat01-fixture-a-r" {
			t.Fatalf("%v left: token after the refresh = %q, want the rotated one", left, got)
		}
	}
}

func TestAFailedLoginRefreshKeepsTheTokenAndIsRetried(t *testing.T) {
	fixture := newRefreshFixture(t, time.Now().Add(10*time.Minute))
	fixture.fail = true
	fixture.refresh.Step(context.Background(), time.Now())
	if got := fixture.token(t); got != "sk-ant-oat01-fixture-a" || fixture.rotations != 0 {
		t.Fatalf("after a failed refresh: token %q, %d stored; want the old one kept", got, fixture.rotations)
	}
	fixture.fail = false
	fixture.refresh.Step(context.Background(), time.Now())
	if fixture.refreshes != 2 || fixture.token(t) != "sk-ant-oat01-fixture-a-r" {
		t.Fatalf("the retry: %d refreshes, token %q; want it rotated on the second", fixture.refreshes, fixture.token(t))
	}
}

func TestLoginRefreshWithNoLoginDoesNothing(t *testing.T) {
	fixture := newRefreshFixture(t, time.Now())
	if err := fixture.settings.Set(context.Background(), store.SettingCloudAccount, ""); err != nil {
		t.Fatal(err)
	}
	fixture.refresh.Step(context.Background(), time.Now())
	if fixture.refreshes != 0 {
		t.Fatalf("refreshed %d times with no login", fixture.refreshes)
	}
}
