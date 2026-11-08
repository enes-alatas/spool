package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"regexp"
)

// ManagementTokenPrefix starts every management token, so a rule can recognise one by
// its shape where the hub, holding only its hash, can't by its value
// (ADR-0067).
const ManagementTokenPrefix = "spool_manage_"

// ManagementTokenShape matches a management token: the prefix and 43 characters of
// unpadded base64url, the 32 random bytes NewManagementToken writes.
var ManagementTokenShape = regexp.MustCompile(ManagementTokenPrefix + `[A-Za-z0-9_-]{43}`)

// managementTokenWhole is ManagementTokenShape anchored, for telling a token from a
// string that merely holds one.
var managementTokenWhole = regexp.MustCompile(`^` + ManagementTokenShape.String() + `$`)

// NewManagementToken mints a management token: 32 bytes from crypto/rand behind
// ManagementTokenPrefix (ADR-0067).
func NewManagementToken() string {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		panic("store: crypto/rand unavailable: " + err.Error())
	}
	return ManagementTokenPrefix + base64.RawURLEncoding.EncodeToString(random)
}

// IsManagementToken reports whether token has a management token's shape, whole.
func IsManagementToken(token string) bool { return managementTokenWhole.MatchString(token) }

// ManagementToken is one token an operator's agent holds to manage the hub
// (ADR-0067). The store keeps only the token's SHA-256, never the value.
// OwnerUserID is "" for one the operator token minted, which belongs to no
// user. Times are Unix milliseconds; ExpiresAt is 0 for a token with no end,
// and LastUsedAt 0 for one never used.
type ManagementToken struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	LastUsedAt  int64  `json:"last_used_at"`
}

// ManagementTouchEvery is how often a token's use is written at most: calls on
// a public listener then don't each write to the database (ADR-0067).
const ManagementTouchEvery = int64(60_000)

type ManagementTokenStore interface {
	// Mint stores a new token under managementToken's name and returns it, once:
	// only its hash is kept. ErrDuplicate if the name is taken.
	Mint(ctx context.Context, managementToken *ManagementToken) (string, error)
	// List is every token, by name.
	List(ctx context.Context) ([]*ManagementToken, error)
	// Get is ErrNotFound for an unknown id.
	Get(ctx context.Context, id string) (*ManagementToken, error)
	// GetByToken finds the token whose value is token, whatever its end;
	// ErrNotFound for one the store doesn't hold.
	GetByToken(ctx context.Context, token string) (*ManagementToken, error)
	// Touch records the token's use at the time at, unless it was recorded
	// less than ManagementTouchEvery before.
	Touch(ctx context.Context, id string, at int64) error
	// Delete revokes a token. ErrNotFound for an unknown id.
	Delete(ctx context.Context, id string) error
}
