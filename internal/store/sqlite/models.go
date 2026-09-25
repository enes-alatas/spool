package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

type models struct{ db *sql.DB }

func (r models) Resolutions(ctx context.Context) ([]*store.ModelResolution, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT model, resolved, source, cli_version, resolved_at FROM model_resolutions ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.ModelResolution
	for rows.Next() {
		var res store.ModelResolution
		if err := rows.Scan(&res.Model, &res.Resolved, &res.Source, &res.CLIVersion, &res.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, &res)
	}
	return out, rows.Err()
}

func (r models) SetResolution(ctx context.Context, res *store.ModelResolution) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO model_resolutions (model, resolved, source, cli_version, resolved_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(model) DO UPDATE SET resolved=excluded.resolved, source=excluded.source,
			cli_version=excluded.cli_version, resolved_at=excluded.resolved_at`,
		res.Model, res.Resolved, res.Source, res.CLIVersion, res.ResolvedAt)
	return err
}

const customModelCols = `id, model, label, created_at`

func scanCustomModel(row interface{ Scan(...any) error }) (*store.CustomModel, error) {
	var m store.CustomModel
	if err := row.Scan(&m.ID, &m.Model, &m.Label, &m.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

func (r models) ListCustom(ctx context.Context) ([]*store.CustomModel, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+customModelCols+` FROM custom_models ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.CustomModel
	for rows.Next() {
		m, err := scanCustomModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r models) AddCustom(ctx context.Context, m *store.CustomModel) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO custom_models (`+customModelCols+`) VALUES (?,?,?,?)`,
		m.ID, m.Model, m.Label, m.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	return err
}

func (r models) SetCustomLabel(ctx context.Context, id, label string) (*store.CustomModel, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE custom_models SET label=? WHERE id=?`, label, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, store.ErrNotFound
	}
	return scanCustomModel(r.db.QueryRowContext(ctx, `SELECT `+customModelCols+` FROM custom_models WHERE id=?`, id))
}

// DeleteCustom takes the entry's resolution with it, in one transaction: a
// resolution outliving its entry would come back with the next entry of the
// same name as if it had been checked.
func (r models) DeleteCustom(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var model string
	if err := tx.QueryRowContext(ctx, `SELECT model FROM custom_models WHERE id=?`, id).Scan(&model); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM custom_models WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_resolutions WHERE model=?`, model); err != nil {
		return err
	}
	return tx.Commit()
}
