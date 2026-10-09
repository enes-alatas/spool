package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// users is the hub's users (ADR-0048).
type users struct{ db *sql.DB }

const userCols = `id, name, role, password_hash, must_change_password, created_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanUser(row rowScanner) (*store.User, error) {
	var user store.User
	err := row.Scan(&user.ID, &user.Name, &user.Role, &user.PasswordHash, &user.MustChangePassword, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (table users) Create(ctx context.Context, user *store.User) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO users (`+userCols+`) VALUES (?,?,?,?,?,?)`,
		user.ID, user.Name, user.Role, user.PasswordHash, user.MustChangePassword, user.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	return err
}

func (table users) Get(ctx context.Context, id string) (*store.User, error) {
	return scanUser(table.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

func (table users) GetByName(ctx context.Context, name string) (*store.User, error) {
	return scanUser(table.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE name=?`, name))
}

func (table users) List(ctx context.Context) ([]*store.User, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	return out, rows.Err()
}

func (table users) SetPassword(ctx context.Context, id, hash string, mustChange bool) error {
	return affectedOne(table.db.ExecContext(ctx, `UPDATE users SET password_hash=?, must_change_password=?
		WHERE id=?`, hash, mustChange, id))
}

func (table users) Delete(ctx context.Context, id string) error {
	return affectedOne(table.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id))
}

// affectedOne maps an update or delete that matched no row to ErrNotFound.
func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if count, err := res.RowsAffected(); err == nil && count == 0 {
		return store.ErrNotFound
	}
	return nil
}

// userSessions is the control room's sign-ins (ADR-0048).
type userSessions struct{ db *sql.DB }

func (table userSessions) Create(ctx context.Context, session *store.UserSession) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO user_sessions (id_hash, user_id, token_hash, created_at, last_seen_at)
		VALUES (?,?,?,?,?)`, session.IDHash, nullIfEmpty(session.UserID), nullIfEmpty(session.TokenHash),
		session.CreatedAt, session.LastSeenAt)
	return err
}

func (table userSessions) Get(ctx context.Context, idHash string) (*store.UserSession, error) {
	var session store.UserSession
	var userID, tokenHash sql.NullString
	err := table.db.QueryRowContext(ctx, `SELECT id_hash, user_id, token_hash, created_at, last_seen_at
		FROM user_sessions WHERE id_hash=?`, idHash).Scan(&session.IDHash, &userID, &tokenHash,
		&session.CreatedAt, &session.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	session.UserID, session.TokenHash = userID.String, tokenHash.String
	return &session, err
}

func (table userSessions) Touch(ctx context.Context, idHash string, at int64) error {
	_, err := table.db.ExecContext(ctx, `UPDATE user_sessions SET last_seen_at=? WHERE id_hash=?`, at, idHash)
	return err
}

func (table userSessions) Delete(ctx context.Context, idHash string) error {
	_, err := table.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE id_hash=?`, idHash)
	return err
}

func (table userSessions) DeleteForUser(ctx context.Context, userID, keepIDHash string) error {
	_, err := table.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id=? AND id_hash<>?`, userID, keepIDHash)
	return err
}

func (table userSessions) DeleteExpired(ctx context.Context, lastSeenBefore, createdBefore int64) (int64, error) {
	res, err := table.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE last_seen_at<? OR created_at<?`,
		lastSeenBefore, createdBefore)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// signInThrottles is the per-username sign-in throttle (ADR-0048).
type signInThrottles struct{ db *sql.DB }

func (table signInThrottles) Get(ctx context.Context, name string) (*store.SignInThrottle, error) {
	var throttle store.SignInThrottle
	err := table.db.QueryRowContext(ctx, `SELECT name, failures, locked_until, last_failure_at
		FROM sign_in_throttle WHERE name=?`, name).Scan(&throttle.Name, &throttle.Failures,
		&throttle.LockedUntil, &throttle.LastFailureAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return &throttle, err
}

func (table signInThrottles) Fail(ctx context.Context, name string, at int64) (int, error) {
	var failures int
	err := table.db.QueryRowContext(ctx, `INSERT INTO sign_in_throttle (name, failures, last_failure_at)
		VALUES (?,1,?) ON CONFLICT(name) DO UPDATE SET failures=failures+1, last_failure_at=excluded.last_failure_at
		RETURNING failures`, name, at).Scan(&failures)
	return failures, err
}

func (table signInThrottles) Lock(ctx context.Context, name string, until int64) error {
	_, err := table.db.ExecContext(ctx, `UPDATE sign_in_throttle SET locked_until=MAX(locked_until, ?) WHERE name=?`,
		until, name)
	return err
}

func (table signInThrottles) Clear(ctx context.Context, name string) error {
	_, err := table.db.ExecContext(ctx, `DELETE FROM sign_in_throttle WHERE name=?`, name)
	return err
}

func (table signInThrottles) DeleteBefore(ctx context.Context, cutoff int64) (int64, error) {
	res, err := table.db.ExecContext(ctx, `DELETE FROM sign_in_throttle WHERE last_failure_at<?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
