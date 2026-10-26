package loop

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// RefreshWindow is how long before its expiry a Claude login's access
// token is refreshed. The token lasts eight hours; an hour's margin leaves
// a failing refresh sixty tries before the loops lose their login
// (ADR-0058).
const RefreshWindow = time.Hour

// refreshEvery is how often the hub looks at the token, and retries a
// refresh that failed.
const refreshEvery = time.Minute

// RefreshAccount refreshes a Claude login and returns the rotated
// sign-in. The host's claude does it (ADR-0058).
type RefreshAccount func(ctx context.Context, account store.CloudAccount) (store.CloudAccount, error)

// LoginRefresh keeps every Claude login's access token fresh, each on its
// own (ADR-0060). Every loop that runs on a login reads it from the store
// at each use, contained loops through the Claude proxy and cloud loops
// through the hub's calls, so a refresh reaches them all at once and holds
// none of them. A refresh revokes the token it replaces, which a request
// already under way has used (ADR-0058).
type LoginRefresh struct {
	logins  store.ClaudeLoginStore
	refresh RefreshAccount
	log     *slog.Logger
	// rotated runs once a rotated token is stored: the redactor learns it
	// there.
	rotated func(ctx context.Context)

	// held is each login's lock, held across its refresh, from reading the
	// login to storing the rotated one. A sign-in, a Sign out or a removal
	// takes it too, through Lock, so a refresh never writes back over a
	// login changed meanwhile.
	mu   sync.Mutex
	held map[string]*sync.Mutex
}

// NewLoginRefresh returns the refresh of the Claude logins in logins.
// rotated runs after each refresh is stored.
func NewLoginRefresh(logins store.ClaudeLoginStore, refresh RefreshAccount, rotated func(ctx context.Context), log *slog.Logger) *LoginRefresh {
	return &LoginRefresh{logins: logins, refresh: refresh, rotated: rotated, log: log, held: map[string]*sync.Mutex{}}
}

// Lock and Unlock keep a refresh of the login called name from running
// while it is being replaced or removed.
func (lr *LoginRefresh) Lock(name string)   { lr.lockOf(name).Lock() }
func (lr *LoginRefresh) Unlock(name string) { lr.lockOf(name).Unlock() }

func (lr *LoginRefresh) lockOf(name string) *sync.Mutex {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lock, ok := lr.held[name]
	if !ok {
		lock = &sync.Mutex{}
		lr.held[name] = lock
	}
	return lock
}

// Run looks at the tokens at once, then every refreshEvery until ctx ends.
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

// Step refreshes each login whose token expires within RefreshWindow of
// now. A refresh that fails keeps that login's token, to be tried again
// at the next step, and the others go ahead.
func (lr *LoginRefresh) Step(ctx context.Context, now time.Time) {
	logins, err := lr.logins.List(ctx)
	if err != nil {
		lr.log.Warn("Claude login refresh: the logins can't be read", "err", err)
		return
	}
	for _, login := range logins {
		if due(login.Account, now) {
			lr.refreshLogin(ctx, login.Name, now)
		}
	}
}

// due reports whether account is signed in and expires within
// RefreshWindow of now.
func due(account store.CloudAccount, now time.Time) bool {
	return account.Token != "" && account.RefreshToken != "" && account.ExpiresAt != 0 &&
		time.UnixMilli(account.ExpiresAt).Sub(now) <= RefreshWindow
}

// refreshLogin refreshes the login called name under its lock, reading it
// again there: a sign-in or Sign out may have changed it since Step
// listed it.
func (lr *LoginRefresh) refreshLogin(ctx context.Context, name string, now time.Time) {
	lr.Lock(name)
	defer lr.Unlock(name)
	account, signedIn, err := store.SignedInAccount(ctx, lr.logins, name)
	if err != nil {
		lr.log.Warn("Claude login refresh: the login can't be read", "login", name, "err", err)
		return
	}
	if !signedIn || !due(account, now) {
		return
	}
	refreshed, err := lr.refresh(ctx, account)
	if err != nil {
		lr.log.Warn("Claude login refresh failed; retrying in a minute", "login", name, "err", err)
		return
	}
	if err := lr.logins.SetAccount(ctx, name, refreshed); err != nil {
		// the old token is revoked already: the loops are refused until
		// someone signs in again
		lr.log.Error("Claude login refresh: the rotated token can't be stored", "login", name, "err", err)
		return
	}
	if lr.rotated != nil {
		lr.rotated(ctx)
	}
	lr.log.Info("Claude login refreshed", "login", name, "expires_at", time.UnixMilli(refreshed.ExpiresAt).UTC().Format(time.RFC3339))
}
