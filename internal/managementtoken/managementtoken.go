// Package managementtoken checks the token an operator's agent presents to the
// hub (ADR-0067). The token is a credential of its own: it is never the
// operator token, a user's session or a loop token, and no route of the
// operator API takes it. The management MCP (#1404) is the one place
// that calls Verify.
package managementtoken

import (
	"context"
	"errors"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// DefaultLife is how long a token lasts when its owner picks no end at mint
// (ADR-0067, the operator's decision of 2026-11-08).
const DefaultLife = 90 * 24 * time.Hour

// ErrRefused is every reason a token is not taken: a string not shaped as
// one, a token the hub doesn't hold (never minted, or revoked), or one past
// its end. They answer alike, so a caller learns nothing about which.
var ErrRefused = errors.New("managementtoken: refused")

// ActorPrefix starts every management token's actor on the change record, so a
// rule can tell a management token's change from a person's.
const ActorPrefix = "manage:"

// Identity is who a token it takes speaks for.
type Identity struct {
	ID   string
	Name string
}

// Actor names the token on a loop's change record, by its name and its id,
// so a token minted later under a revoked one's name is told apart
// (ADR-0067).
func (identity Identity) Actor() string {
	return ActorPrefix + identity.Name + "#" + identity.ID
}

// Checker checks management tokens against the store's. The hub wires one and
// hands it to the management MCP.
type Checker struct {
	Tokens store.ManagementTokenStore
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Verify returns who token speaks for, or ErrRefused. A token it takes has
// its use recorded, at most once a minute: within the minute it writes
// nothing at all (Touch). Any other error is the store's.
func (checker *Checker) Verify(ctx context.Context, token string) (Identity, error) {
	if !store.IsManagementToken(token) {
		return Identity{}, ErrRefused
	}
	managementToken, err := checker.Tokens.GetByToken(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return Identity{}, ErrRefused
	}
	if err != nil {
		return Identity{}, err
	}
	now := time.Now
	if checker.Now != nil {
		now = checker.Now
	}
	at := now().UnixMilli()
	if managementToken.ExpiresAt != 0 && at >= managementToken.ExpiresAt {
		return Identity{}, ErrRefused
	}
	// Most calls fall within the minute: they leave the write out, so a
	// valid token's calls don't each take the database's write lock.
	// Touch's own guard still holds when two calls race.
	if at-managementToken.LastUsedAt < store.ManagementTouchEvery {
		return Identity{ID: managementToken.ID, Name: managementToken.Name}, nil
	}
	if err := checker.Tokens.Touch(ctx, managementToken.ID, at); err != nil {
		return Identity{}, err
	}
	return Identity{ID: managementToken.ID, Name: managementToken.Name}, nil
}
