package sqlite

import (
	"context"
	"database/sql"

	"github.com/enes-alatas/spool/internal/store"
)

// loopChanges is the record of who changed which loop (ADR-0048).
type loopChanges struct{ db *sql.DB }

func (table loopChanges) Record(ctx context.Context, change *store.LoopChange) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO loop_changes (at, actor, action, loop_id, loop_name) VALUES (?,?,?,?,?)`,
		change.At, change.By, change.Action, change.LoopID, change.LoopName)
	if err != nil {
		return err
	}
	if change.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	switch change.Action {
	case store.LoopChangeWake:
	case store.LoopChangeCreate:
		_, err = tx.ExecContext(ctx, `UPDATE loops SET created_by=?, updated_by=? WHERE id=?`, change.By, change.By, change.LoopID)
	default:
		_, err = tx.ExecContext(ctx, `UPDATE loops SET updated_by=? WHERE id=?`, change.By, change.LoopID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (table loopChanges) List(ctx context.Context, filter store.LoopChangeFilter) ([]*store.LoopChange, error) {
	query := `SELECT id, at, actor, action, loop_id, loop_name FROM loop_changes WHERE 1=1`
	var args []any
	if filter.LoopID != "" {
		query += ` AND loop_id=?`
		args = append(args, filter.LoopID)
	}
	if filter.LoopName != "" {
		query += ` AND loop_name=?`
		args = append(args, filter.LoopName)
	}
	query += ` ORDER BY id DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	rows, err := table.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := []*store.LoopChange{}
	for rows.Next() {
		var change store.LoopChange
		if err := rows.Scan(&change.ID, &change.At, &change.By, &change.Action, &change.LoopID, &change.LoopName); err != nil {
			return nil, err
		}
		changes = append(changes, &change)
	}
	return changes, rows.Err()
}
