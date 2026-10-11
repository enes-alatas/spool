package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// userLinks is the hub's users' links to the people they are on the
// surfaces, the codes and confirmations that make them (#703, ADR-0051),
// and the record of links made and removed.
type userLinks struct{ db *sql.DB }

func (table userLinks) SetCode(ctx context.Context, userID, surface, codeHash string, expiresAt int64) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO link_codes (code_hash, user_id, surface, expires_at) VALUES (?,?,?,?)
		ON CONFLICT (user_id) DO UPDATE SET code_hash=excluded.code_hash, surface=excluded.surface, expires_at=excluded.expires_at`,
		codeHash, userID, surface, expiresAt)
	return err
}

func (table userLinks) Claim(ctx context.Context, codeHash string, confirmation *store.LinkConfirmation, now int64) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID, surface string
	var expiresAt int64
	err = tx.QueryRowContext(ctx, `DELETE FROM link_codes WHERE code_hash=? RETURNING user_id, surface, expires_at`, codeHash).
		Scan(&userID, &surface, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	} else if err != nil {
		return err
	}
	switch holder, holderErr := linkHolder(ctx, tx, confirmation.Person); {
	case expiresAt < now || surface != store.PersonSurface(confirmation.Person):
		err = store.ErrNotFound
	case holderErr != nil:
		return holderErr
	case holder != "" && holder != userID:
		err = store.ErrDuplicate
	default:
		confirmation.UserID = userID
		_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO link_confirmations
			(person, user_id, loop_id, username, display, team_id, expires_at) VALUES (?,?,?,?,?,?,?)`,
			confirmation.Person, userID, confirmation.LoopID, confirmation.Username, confirmation.Display,
			confirmation.TeamID, confirmation.ExpiresAt)
		if err != nil {
			return err
		}
	}
	// The code is spent whatever became of it.
	if commitErr := tx.Commit(); commitErr != nil {
		return commitErr
	}
	return err
}

func (table userLinks) Confirm(ctx context.Context, person string, now int64) (*store.UserLink, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var confirmation store.LinkConfirmation
	err = tx.QueryRowContext(ctx, `DELETE FROM link_confirmations WHERE person=?
		RETURNING person, user_id, loop_id, username, display, team_id, expires_at`, person).
		Scan(&confirmation.Person, &confirmation.UserID, &confirmation.LoopID, &confirmation.Username,
			&confirmation.Display, &confirmation.TeamID, &confirmation.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	link := &store.UserLink{UserID: confirmation.UserID, Surface: store.PersonSurface(person), Person: person, CreatedAt: now}
	switch holder, holderErr := linkHolder(ctx, tx, person); {
	case confirmation.ExpiresAt < now:
		err = store.ErrNotFound
	case holderErr != nil:
		return nil, holderErr
	case holder != "" && holder != link.UserID:
		err = store.ErrDuplicate
	}
	if err != nil {
		// The confirmation is spent whatever became of it.
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, commitErr
		}
		return nil, err
	}
	if err := table.link(ctx, tx, link, &confirmation); err != nil {
		return nil, err
	}
	return link, tx.Commit()
}

// linkHolder is the user person is linked to, or "".
func linkHolder(ctx context.Context, tx *sql.Tx, person string) (string, error) {
	var userID string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM user_links WHERE person=?`, person).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return userID, err
}

// link writes link in place of its user's link on its surface, makes its
// person an allowed sender, as confirmation names them, and records it. A
// link it replaces to another person is removed as Delete removes one.
func (table userLinks) link(ctx context.Context, tx *sql.Tx, link *store.UserLink, confirmation *store.LinkConfirmation) error {
	var replaced string
	err := tx.QueryRowContext(ctx, `SELECT person FROM user_links WHERE user_id=? AND surface=?`,
		link.UserID, link.Surface).Scan(&replaced)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case replaced != link.Person:
		if _, err := unlink(ctx, tx, link.UserID, link.Surface, link.CreatedAt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO user_links (user_id, surface, person, created_at) VALUES (?,?,?,?)`,
		link.UserID, link.Surface, link.Person, link.CreatedAt); err != nil {
		return err
	}
	if err := allow(ctx, tx, confirmation, link.CreatedAt); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO link_changes (at, action, user_name, surface, person, loop_name)
		VALUES (?, ?, COALESCE((SELECT name FROM users WHERE id=?), ''), ?, ?, COALESCE((SELECT name FROM loops WHERE id=?), ''))`,
		link.CreatedAt, store.LinkChangeLink, link.UserID, link.Surface, link.Person, confirmation.LoopID)
	return err
}

// allow makes the person confirmation names an allowed sender on their
// surface: confirming a link is how a stranger becomes one, a pending
// sender from before ADR-0051 included. An allowed sender is left as they
// are, and a blocked one too, since nothing a person sends lifts a block.
func allow(ctx context.Context, tx *sql.Tx, confirmation *store.LinkConfirmation, now int64) error {
	surface, id := personID(confirmation.Person)
	var err error
	switch surface {
	case store.SurfaceTelegram:
		_, err = tx.ExecContext(ctx, `INSERT INTO tg_senders (tg_user_id, username, display, status, created_at, updated_at)
			VALUES (?,?,?,?,?,?) ON CONFLICT (tg_user_id) DO UPDATE SET status=excluded.status, updated_at=excluded.updated_at WHERE status='pending'`,
			id, confirmation.Username, confirmation.Display, store.SenderAllowed, now, now)
	case store.SurfaceSlack:
		_, err = tx.ExecContext(ctx, `INSERT INTO slack_senders (slack_user_id, team_id, username, display, status, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?) ON CONFLICT (slack_user_id) DO UPDATE SET status=excluded.status, updated_at=excluded.updated_at WHERE status='pending'`,
			id, confirmation.TeamID, confirmation.Username, confirmation.Display, store.SenderAllowed, now, now)
	default:
		err = errors.New("a link is to a person on a surface: " + confirmation.Person)
	}
	return err
}

// unlink removes the user's link on surface and the sender it allowed,
// records it, and answers with the person it was to. ErrNotFound if there
// is none.
func unlink(ctx context.Context, tx *sql.Tx, userID, surface string, now int64) (string, error) {
	var person string
	err := tx.QueryRowContext(ctx, `DELETE FROM user_links WHERE user_id=? AND surface=? RETURNING person`,
		userID, surface).Scan(&person)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	} else if err != nil {
		return "", err
	}
	// The person's row goes, unless it is blocked: a block outlives the
	// link, and deleting the row would lift it. An owner is an allowed
	// sender, so the loops the person owns are theirs no longer, as when
	// an admin deletes a sender.
	_, id := personID(person)
	var disallow, disown string
	switch surface {
	case store.SurfaceTelegram:
		disallow = `DELETE FROM tg_senders WHERE tg_user_id=? AND status<>?`
		disown = `UPDATE loops SET owner_tg_user_id=0, owner_dm_chat_id=0, updated_at=? WHERE owner_tg_user_id=?`
	case store.SurfaceSlack:
		disallow = `DELETE FROM slack_senders WHERE slack_user_id=? AND status<>?`
		disown = `UPDATE loops SET owner_slack_user_id='', owner_slack_dm_channel='', updated_at=? WHERE owner_slack_user_id=?`
	default:
		return "", errors.New("a link is to a person on a surface: " + person)
	}
	if _, err := tx.ExecContext(ctx, disallow, id, store.SenderBlocked); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, disown, now, id); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO link_changes (at, action, user_name, surface, person)
		VALUES (?, ?, COALESCE((SELECT name FROM users WHERE id=?), ''), ?, ?)`,
		now, store.LinkChangeUnlink, userID, surface, person)
	return person, err
}

// personID splits a TGPerson or SlackPerson key into its surface and the
// id the surface's senders are kept by.
func personID(person string) (surface, id string) {
	surface = store.PersonSurface(person)
	_, id, _ = strings.Cut(person, ":")
	return surface, id
}

func (table userLinks) Waiting(ctx context.Context, userID string, now int64) ([]*store.LinkConfirmation, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT person, user_id, loop_id, username, display, team_id, expires_at
		FROM link_confirmations WHERE user_id=? AND expires_at>=? ORDER BY person`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.LinkConfirmation{}
	for rows.Next() {
		var confirmation store.LinkConfirmation
		if err := rows.Scan(&confirmation.Person, &confirmation.UserID, &confirmation.LoopID, &confirmation.Username,
			&confirmation.Display, &confirmation.TeamID, &confirmation.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, &confirmation)
	}
	return out, rows.Err()
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

func (table userLinks) Delete(ctx context.Context, userID, surface string, now int64) (*store.UserLink, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	person, err := unlink(ctx, tx, userID, surface, now)
	if err != nil {
		return nil, err
	}
	return &store.UserLink{UserID: userID, Surface: surface, Person: person}, tx.Commit()
}

func (table userLinks) Changes(ctx context.Context, limit int) ([]*store.LinkChange, error) {
	query := `SELECT id, at, action, user_name, surface, person, loop_name FROM link_changes ORDER BY id DESC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := table.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := []*store.LinkChange{}
	for rows.Next() {
		var change store.LinkChange
		if err := rows.Scan(&change.ID, &change.At, &change.Action, &change.User, &change.Surface,
			&change.Person, &change.LoopName); err != nil {
			return nil, err
		}
		changes = append(changes, &change)
	}
	return changes, rows.Err()
}
