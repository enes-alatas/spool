package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// connections keeps each connection's config as its kind's JSON, so a kind
// that needs another field does not need another column, and its loops as
// connection_loops rows (ADR-0043).
// Each value is sealed under the hub key (ADR-0046).
type connections struct {
	db     *sql.DB
	sealer *box
}

const connectionColumns = `name, kind, config, secret, created_at, updated_at, owner_loop, rotated_at, revoked_at`

func (table connections) List(ctx context.Context) ([]*store.Connection, error) {
	return table.list(ctx, `SELECT `+connectionColumns+` FROM connections ORDER BY name`)
}

func (table connections) ListByLoop(ctx context.Context, loopID string) ([]*store.Connection, error) {
	return table.list(ctx, `SELECT `+connectionColumns+` FROM connections
		WHERE name IN (SELECT connection FROM connection_loops WHERE loop_id=?) ORDER BY name`, loopID)
}

func (table connections) list(ctx context.Context, query string, args ...any) ([]*store.Connection, error) {
	rows, err := table.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []*store.Connection
	for rows.Next() {
		connection, err := table.scanConnection(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, connection)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, connection := range out {
		if connection.LoopIDs, err = table.loopIDs(ctx, connection.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (table connections) Get(ctx context.Context, name string) (*store.Connection, error) {
	connection, err := table.scanConnection(table.db.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM connections WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if connection.LoopIDs, err = table.loopIDs(ctx, name); err != nil {
		return nil, err
	}
	return connection, nil
}

func (table connections) loopIDs(ctx context.Context, name string) ([]string, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT loop_id FROM connection_loops WHERE connection=? ORDER BY loop_id`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (table connections) Create(ctx context.Context, connection *store.Connection) error {
	config, err := json.Marshal(connection.Config)
	if err != nil {
		return err
	}
	if connection.UpdatedAt == 0 {
		connection.UpdatedAt = connection.CreatedAt
	}
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO connections (`+connectionColumns+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		connection.Name, connection.Kind, string(config), table.sealer.seal(connection.Secret), connection.CreatedAt, connection.UpdatedAt,
		nullable(connection.OwnerLoopID), connection.RotatedAt, connection.RevokedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	if err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, store.ConnectionEventCreate, connection.Name, "", connection.CreatedAt); err != nil {
		return err
	}
	connection.LoopIDs = []string{}
	if connection.OwnerLoopID != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO connection_loops (connection, loop_id, attached_at) VALUES (?,?,?)`,
			connection.Name, connection.OwnerLoopID, connection.CreatedAt)
		if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, store.ConnectionEventAttach, connection.Name, connection.OwnerLoopID, connection.CreatedAt); err != nil {
			return err
		}
		connection.LoopIDs = []string{connection.OwnerLoopID}
	}
	return tx.Commit()
}

func (table connections) Delete(ctx context.Context, name string, at int64) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner, err := connectionOwner(ctx, tx, name)
	if err != nil {
		return err
	}
	var attached bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM connection_loops WHERE connection=? AND loop_id<>?)`,
		name, owner).Scan(&attached); err != nil {
		return err
	}
	if attached {
		return store.ErrConnectionAttached
	}
	if owner != "" {
		if err := detachRecorded(ctx, tx, name, owner, at); err != nil {
			return err
		}
	}
	if err := retireSecrets(ctx, tx, `name = ?1`, at, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM connections WHERE name=?`, name); err != nil {
		return err
	}
	// a private one's owner, so the loop's record shows it gone
	if err := recordEvent(ctx, tx, store.ConnectionEventDelete, name, owner, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (table connections) Share(ctx context.Context, name string, at int64) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner, err := liveConnectionOwner(ctx, tx, name)
	if err != nil || owner == "" {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE connections SET owner_loop=NULL WHERE name=?`, name); err != nil {
		return err
	}
	// the loop it was private to, so its record says what it gave away
	if err := recordEvent(ctx, tx, store.ConnectionEventShare, name, owner, at); err != nil {
		return err
	}
	return tx.Commit()
}

// connectionOwner is a connection's owner loop, "" for a shared one.
// ErrNotFound for an unknown name.
func connectionOwner(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	var owner sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT owner_loop FROM connections WHERE name=?`, name).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return owner.String, err
}

// liveConnectionOwner is connectionOwner for a write a revoked connection
// refuses: ErrConnectionRevoked once it is.
func liveConnectionOwner(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	var owner sql.NullString
	var revokedAt int64
	err := tx.QueryRowContext(ctx, `SELECT owner_loop, revoked_at FROM connections WHERE name=?`, name).Scan(&owner, &revokedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", store.ErrNotFound
	case err != nil:
		return "", err
	case revokedAt != 0:
		return "", store.ErrConnectionRevoked
	}
	return owner.String, nil
}

func (table connections) SetSecret(ctx context.Context, name, secret string, at int64) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := liveConnectionOwner(ctx, tx, name); err != nil {
		return err
	}
	// Compared opened, in Go: two seals of one value never match.
	var sealed string
	if err := tx.QueryRowContext(ctx, `SELECT secret FROM connections WHERE name=?`, name).Scan(&sealed); err != nil {
		return err
	}
	current, err := table.sealer.open(sealed)
	if err != nil {
		return err
	}
	if current == secret {
		return nil // it holds this value already
	}
	// before the update, which is what it reads the old value from
	if err := retireSecrets(ctx, tx, `name = ?1`, at, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE connections SET secret=?, updated_at=?, rotated_at=? WHERE name=?`,
		table.sealer.seal(secret), at, at, name); err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, store.ConnectionEventRotate, name, "", at); err != nil {
		return err
	}
	return tx.Commit()
}

func (table connections) Attach(ctx context.Context, name, loopID string, at int64) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner, err := liveConnectionOwner(ctx, tx, name)
	if err != nil {
		return err
	}
	if owner != "" && owner != loopID {
		return store.ErrConnectionPrivate
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO connection_loops (connection, loop_id, attached_at) VALUES (?,?,?)
		ON CONFLICT (connection, loop_id) DO NOTHING`, name, loopID, at)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil // attached already
	}
	if err := recordEvent(ctx, tx, store.ConnectionEventAttach, name, loopID, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (table connections) Detach(ctx context.Context, name, loopID string, at int64) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := connectionOwner(ctx, tx, name); err != nil {
		return err
	}
	if err := detachRecorded(ctx, tx, name, loopID, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (table connections) Revoke(ctx context.Context, name string, at int64) ([]string, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	owner, err := liveConnectionOwner(ctx, tx, name)
	if err != nil {
		return nil, err
	}
	loopIDs, err := attachedLoops(ctx, tx, name)
	if err != nil {
		return nil, err
	}
	for _, loopID := range loopIDs {
		if err := detachRecorded(ctx, tx, name, loopID, at); err != nil {
			return nil, err
		}
	}
	// before the update, which is what it reads the value from
	if err := retireSecrets(ctx, tx, `name = ?1`, at, name); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE connections SET secret='', revoked_at=? WHERE name=?`, at, name); err != nil {
		return nil, err
	}
	// a private one's owner, so the loop's record shows it gone
	if err := recordEvent(ctx, tx, store.ConnectionEventRevoke, name, owner, at); err != nil {
		return nil, err
	}
	return loopIDs, tx.Commit()
}

// attachedLoops is the ids of the loops holding a connection, read inside
// tx: the store has one connection, so a read outside it would wait on it.
func attachedLoops(ctx context.Context, tx *sql.Tx, name string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT loop_id FROM connection_loops WHERE connection=? ORDER BY loop_id`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// detachRecorded detaches a connection from a loop and records it, or does
// nothing when it isn't attached.
func detachRecorded(ctx context.Context, tx *sql.Tx, name, loopID string, at int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM connection_loops WHERE connection=? AND loop_id=?`, name, loopID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil
	}
	return recordEvent(ctx, tx, store.ConnectionEventDetach, name, loopID, at)
}

// recordEvent appends a change to the connection's record, with the loop's
// name as it is now. loopID is "" for a change no loop is part of.
func recordEvent(ctx context.Context, tx *sql.Tx, action, connection, loopID string, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO connection_events (action, connection, loop_id, loop_name, at)
		VALUES (?, ?, ?, COALESCE((SELECT name FROM loops WHERE id=?), ''), ?)`, action, connection, loopID, loopID, at)
	return err
}

func (table connections) Events(ctx context.Context, filter store.ConnectionEventFilter) ([]*store.ConnectionEvent, error) {
	query := `SELECT id, action, connection, loop_id, loop_name, at FROM connection_events WHERE 1=1`
	var args []any
	for column, value := range map[string]string{"connection": filter.Connection, "loop_id": filter.LoopID, "loop_name": filter.LoopName} {
		if value != "" {
			query += ` AND ` + column + `=?`
			args = append(args, value)
		}
	}
	rows, err := table.db.QueryContext(ctx, query+` ORDER BY id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []*store.ConnectionEvent{}
	for rows.Next() {
		var event store.ConnectionEvent
		if err := rows.Scan(&event.ID, &event.Action, &event.Connection, &event.LoopID, &event.LoopName, &event.At); err != nil {
			return nil, err
		}
		events = append(events, &event)
	}
	return events, rows.Err()
}

// scanConnection reads a connections row, opening its sealed value.
func (table connections) scanConnection(row interface{ Scan(...any) error }) (*store.Connection, error) {
	var connection store.Connection
	var config string
	var owner sql.NullString
	if err := row.Scan(&connection.Name, &connection.Kind, &config, &connection.Secret, &connection.CreatedAt, &connection.UpdatedAt,
		&owner, &connection.RotatedAt, &connection.RevokedAt); err != nil {
		return nil, err
	}
	connection.OwnerLoopID = owner.String
	var err error
	if connection.Secret, err = table.sealer.open(connection.Secret); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(config), &connection.Config); err != nil {
		return nil, err
	}
	return &connection, nil
}

// retireSecrets keeps the values of the connections where matches, about
// to be replaced or deleted, as retired secrets for the redactor. Each value
// is copied as it is stored, sealed. where is
// a condition on connections, its own parameters numbered from ?1 among
// args; at takes the next number. An empty value has nothing to mask. The
// name is store.Connection.RedactName's, spelled in SQL.
func retireSecrets(ctx context.Context, tx *sql.Tx, where string, at int64, args ...any) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO retired_secrets (connection, redact_name, value, retired_at)
		SELECT name, CASE WHEN kind = 'env-var' THEN COALESCE(json_extract(config, '$.env'), '') ELSE 'connection:' || name END, secret, ?%d
		FROM connections WHERE secret <> '' AND (%s) ORDER BY name`, len(args)+1, where), append(args, at)...)
	return err
}

func (table connections) Retired(ctx context.Context) ([]store.RetiredSecret, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT connection, redact_name, value, retired_at FROM retired_secrets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var retired []store.RetiredSecret
	for rows.Next() {
		var secret store.RetiredSecret
		if err := rows.Scan(&secret.Connection, &secret.RedactName, &secret.Value, &secret.RetiredAt); err != nil {
			return nil, err
		}
		var err error
		if secret.Value, err = table.sealer.open(secret.Value); err != nil {
			return nil, err
		}
		retired = append(retired, secret)
	}
	return retired, rows.Err()
}

// nullable stores "" as NULL, which owner_loop reads as shared.
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
