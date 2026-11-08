package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// managementTokens is the tokens an operator's agent holds (ADR-0067), each kept
// by its SHA-256 alone.
type managementTokens struct{ db *sql.DB }

const managementTokenCols = `id, name, owner_user_id, created_at, expires_at, last_used_at`

func scanManagementToken(row rowScanner) (*store.ManagementToken, error) {
	var managementToken store.ManagementToken
	var owner sql.NullString
	err := row.Scan(&managementToken.ID, &managementToken.Name, &owner, &managementToken.CreatedAt, &managementToken.ExpiresAt, &managementToken.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	managementToken.OwnerUserID = owner.String
	return &managementToken, nil
}

func (table managementTokens) Mint(ctx context.Context, managementToken *store.ManagementToken) (string, error) {
	if managementToken.ID == "" {
		managementToken.ID = "manage_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	minted := store.NewManagementToken()
	_, err := table.db.ExecContext(ctx, `INSERT INTO management_tokens (id, name, owner_user_id, token_hash, created_at, expires_at, last_used_at)
		VALUES (?,?,?,?,?,?,0)`, managementToken.ID, managementToken.Name, nullIfEmpty(managementToken.OwnerUserID), tokenHash(minted),
		managementToken.CreatedAt, managementToken.ExpiresAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return "", store.ErrDuplicate
	}
	if err != nil {
		return "", err
	}
	managementToken.LastUsedAt = 0
	return minted, nil
}

func (table managementTokens) List(ctx context.Context) ([]*store.ManagementToken, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+managementTokenCols+` FROM management_tokens ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.ManagementToken{}
	for rows.Next() {
		managementToken, err := scanManagementToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, managementToken)
	}
	return out, rows.Err()
}

func (table managementTokens) Get(ctx context.Context, id string) (*store.ManagementToken, error) {
	return scanManagementToken(table.db.QueryRowContext(ctx, `SELECT `+managementTokenCols+` FROM management_tokens WHERE id=?`, id))
}

func (table managementTokens) GetByToken(ctx context.Context, token string) (*store.ManagementToken, error) {
	if token == "" {
		return nil, store.ErrNotFound
	}
	return scanManagementToken(table.db.QueryRowContext(ctx, `SELECT `+managementTokenCols+` FROM management_tokens WHERE token_hash=?`, tokenHash(token)))
}

func (table managementTokens) Touch(ctx context.Context, id string, at int64) error {
	_, err := table.db.ExecContext(ctx, `UPDATE management_tokens SET last_used_at=? WHERE id=? AND last_used_at<=?`,
		at, id, at-store.ManagementTouchEvery)
	return err
}

func (table managementTokens) Delete(ctx context.Context, id string) error {
	return affectedOne(table.db.ExecContext(ctx, `DELETE FROM management_tokens WHERE id=?`, id))
}
