package loop

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// RefreshWindow is how long before its expiry the Claude login's access
// token is refreshed. The token lasts eight hours; an hour's margin leaves
// a failing refresh sixty tries before the loops lose their login
// (ADR-0058).
const RefreshWindow = time.Hour

// refreshEvery is how often the hub looks at the token, and retries a
// refresh that failed.
const refreshEvery = time.Minute

// RefreshAccount refreshes the Claude login and returns the rotated
// sign-in. The host's claude does it (ADR-0058).
type RefreshAccount func(ctx context.Context, account store.CloudAccount) (store.CloudAccount, error)

// LoginRefresh keeps the Claude login's access token fresh. Every loop that
// runs on it reads it from the store at each use, contained loops through
// the Claude proxy and cloud loops through the hub's calls, so a refresh
// reaches them all at once and holds none of them. A refresh revokes the
// token it replaces, which a request already under way has used (ADR-0058).
type LoginRefresh struct {
	settings store.SettingsStore
	refresh  RefreshAccount
	log      *slog.Logger
	// rotated runs once a rotated token is stored: the redactor learns it
	// there.
	rotated func(ctx context.Context)

	// mu is held across a refresh, from reading the login to storing the
	// rotated one. A sign-in or a Sign out takes it too, through Lock, so
	// a refresh never writes back over a login changed meanwhile.
	mu sync.Mutex
}

// NewLoginRefresh returns the refresh of the Claude login stored in
// settings. rotated runs after each refresh is stored.
func NewLoginRefresh(settings store.SettingsStore, refresh RefreshAccount, rotated func(ctx context.Context), log *slog.Logger) *LoginRefresh {
	return &LoginRefresh{settings: settings, refresh: refresh, rotated: rotated, log: log}
}

// Lock and Unlock keep a refresh from running while the login is being
// replaced or removed.
func (lr *LoginRefresh) Lock()   { lr.mu.Lock() }
func (lr *LoginRefresh) Unlock() { lr.mu.Unlock() }

// Run looks at the token at once, then every refreshEvery until ctx ends.
func (lr *LoginRefresh) Run(ctx context.Context) {
	ticker := time.NewTicker(refreshEvery)
	defer ticker.Stop()
	for {
		lr.Step(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Step refreshes the token if it expires within RefreshWindow of now. A
// refresh that fails keeps the token, to be tried again at the next step.
func (lr *LoginRefresh) Step(ctx context.Context, now time.Time) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	account, ok := lr.account(ctx)
	if !ok || account.RefreshToken == "" || account.ExpiresAt == 0 ||
		time.UnixMilli(account.ExpiresAt).Sub(now) > RefreshWindow {
		return
	}
	refreshed, err := lr.refresh(ctx, account)
	if err != nil {
		lr.log.Warn("Claude login refresh failed; retrying in a minute", "err", err)
		return
	}
	raw, err := json.Marshal(refreshed)
	if err != nil {
		lr.log.Error("Claude login refresh", "err", err)
		return
	}
	if err := lr.settings.Set(ctx, store.SettingCloudAccount, string(raw)); err != nil {
		// the old token is revoked already: the loops are refused until
		// someone signs in again
		lr.log.Error("Claude login refresh: the rotated token can't be stored", "err", err)
		return
	}
	if lr.rotated != nil {
		lr.rotated(ctx)
	}
	lr.log.Info("Claude login refreshed", "expires_at", time.UnixMilli(refreshed.ExpiresAt).UTC().Format(time.RFC3339))
}

// account reads the stored login; ok is false when none is signed in or it
// can't be read.
func (lr *LoginRefresh) account(ctx context.Context) (store.CloudAccount, bool) {
	raw, err := lr.settings.Get(ctx, store.SettingCloudAccount)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			lr.log.Warn("Claude login refresh: the login can't be read", "err", err)
		}
		return store.CloudAccount{}, false
	}
	var account store.CloudAccount
	if raw == "" || json.Unmarshal([]byte(raw), &account) != nil || account.Token == "" {
		return store.CloudAccount{}, false
	}
	return account, true
}
