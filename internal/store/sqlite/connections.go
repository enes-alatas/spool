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
// that needs another field does not need another column (ADR-0043).
type connections struct{ db *sql.DB }

const connectionColumns = `name, kind, config, secret, created_at`

func (table connections) List(ctx context.Context) ([]*store.Connection, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+connectionColumns+` FROM connections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.Connection
	for rows.Next() {
		connection, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, connection)
	}
	return out, rows.Err()
}

func (table connections) Get(ctx context.Context, name string) (*store.Connection, error) {
	connection, err := scanConnection(table.db.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM connections WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return connection, err
}

func (table connections) Create(ctx context.Context, connection *store.Connection) error {
	config, err := json.Marshal(connection.Config)
	if err != nil {
		return err
	}
	_, err = table.db.ExecContext(ctx, `INSERT INTO connections (`+connectionColumns+`) VALUES (?,?,?,?,?)`,
		connection.Name, connection.Kind, string(config), connection.Secret, connection.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	return err
}

func (table connections) Delete(ctx context.Context, name string) error {
	res, err := table.db.ExecContext(ctx, `DELETE FROM connections WHERE name=?`, name)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func scanConnection(row interface{ Scan(...any) error }) (*store.Connection, error) {
	var connection store.Connection
	var config string
	if err := row.Scan(&connection.Name, &connection.Kind, &config, &connection.Secret, &connection.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(config), &connection.Config); err != nil {
		return nil, err
	}
	return &connection, nil
}
