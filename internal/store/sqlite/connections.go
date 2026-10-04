package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// connections keeps each connection's config as its kind's JSON, so a kind
// that needs another field does not need another column, and its loops as
// connection_loops rows (ADR-0043).
type connections struct{ db *sql.DB }

const connectionColumns = `name, kind, config, secret, created_at, updated_at`

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
		connection, err := scanConnection(rows)
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
	connection, err := scanConnection(table.db.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM connections WHERE name=?`, name))
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
	_, err = table.db.ExecContext(ctx, `INSERT INTO connections (`+connectionColumns+`) VALUES (?,?,?,?,?,?)`,
		connection.Name, connection.Kind, string(config), connection.Secret, connection.CreatedAt, connection.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	connection.LoopIDs = []string{}
	return err
}

func (table connections) Delete(ctx context.Context, name string) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var attached bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM connection_loops WHERE connection=?)`, name).Scan(&attached); err != nil {
		return err
	}
	if attached {
		return store.ErrConnectionAttached
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM connections WHERE name=?`, name)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return tx.Commit()
}

func (table connections) SetSecret(ctx context.Context, name, secret string, at int64) error {
	res, err := table.db.ExecContext(ctx, `UPDATE connections SET secret=?, updated_at=? WHERE name=?`, secret, at, name)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (table connections) Attach(ctx context.Context, name, loopID string, at int64) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO connection_loops (connection, loop_id, attached_at) VALUES (?,?,?)
		ON CONFLICT (connection, loop_id) DO NOTHING`, name, loopID, at)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return store.ErrNotFound
	}
	return err
}

func (table connections) Detach(ctx context.Context, name, loopID string) error {
	if _, err := table.Get(ctx, name); err != nil {
		return err
	}
	_, err := table.db.ExecContext(ctx, `DELETE FROM connection_loops WHERE connection=? AND loop_id=?`, name, loopID)
	return err
}

func scanConnection(row interface{ Scan(...any) error }) (*store.Connection, error) {
	var connection store.Connection
	var config string
	if err := row.Scan(&connection.Name, &connection.Kind, &config, &connection.Secret, &connection.CreatedAt, &connection.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(config), &connection.Config); err != nil {
		return nil, err
	}
	return &connection, nil
}
