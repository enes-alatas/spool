package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/enes-alatas/spool/internal/store"
)

// userLinks is the hub's users' links to the people they are on the
// surfaces, and the codes that make them (#703).
type userLinks struct{ db *sql.DB }

func (table userLinks) SetCode(ctx context.Context, userID, codeHash string, expiresAt int64) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO link_codes (code_hash, user_id, expires_at) VALUES (?,?,?)
		ON CONFLICT (user_id) DO UPDATE SET code_hash=excluded.code_hash, expires_at=excluded.expires_at`,
		codeHash, userID, expiresAt)
	return err
}

func (table userLinks) Redeem(ctx context.Context, codeHash, person string, now int64) (*store.UserLink, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var userID string
	var expiresAt int64
	err = tx.QueryRowContext(ctx, `DELETE FROM link_codes WHERE code_hash=? RETURNING user_id, expires_at`, codeHash).
		Scan(&userID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	link := &store.UserLink{UserID: userID, Surface: store.PersonSurface(person), Person: person, CreatedAt: now}
	switch {
	case expiresAt < now:
		err = store.ErrNotFound
	case link.Surface == "":
		err = errors.New("a link is to a person on a surface: " + person)
	default:
		err = table.link(ctx, tx, link)
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrDuplicate) {
		// The code is spent whatever became of it.
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, commitErr
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return link, tx.Commit()
}

// link writes link in place of the user's link on its surface, unless
// its person is someone else's.
func (table userLinks) link(ctx context.Context, tx *sql.Tx, link *store.UserLink) error {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM user_links WHERE person=?`, link.Person).Scan(&owner)
	switch {
	case err == nil && owner != link.UserID:
		return store.ErrDuplicate
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_links WHERE user_id=? AND surface=?`,
		link.UserID, link.Surface); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_links (user_id, surface, person, created_at) VALUES (?,?,?,?)`,
		link.UserID, link.Surface, link.Person, link.CreatedAt)
	return err
}

func (table userLinks) List(ctx context.Context, userID string) ([]*store.UserLink, error) {
	rows, err := table.db.QueryContext(ctx,
		`SELECT surface, person, created_at FROM user_links WHERE user_id=? ORDER BY surface`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.UserLink{}
	for rows.Next() {
		link := &store.UserLink{UserID: userID}
		if err := rows.Scan(&link.Surface, &link.Person, &link.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

func (table userLinks) Delete(ctx context.Context, userID, surface string) error {
	result, err := table.db.ExecContext(ctx, `DELETE FROM user_links WHERE user_id=? AND surface=?`, userID, surface)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
