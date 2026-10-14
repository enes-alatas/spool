package sqlite

import (
	"context"
	"database/sql"

	"github.com/enes-alatas/spool/internal/store"
)

type problems struct{ db *sql.DB }

func (table problems) Open(ctx context.Context, loopID, symptom string, since int64) (bool, error) {
	result, err := table.db.ExecContext(ctx, `INSERT INTO loop_problems (loop_id, symptom, since) VALUES (?,?,?)
		ON CONFLICT (loop_id, symptom) WHERE cleared_at = 0 DO NOTHING`, loopID, symptom, since)
	if err != nil {
		return false, err
	}
	opened, err := result.RowsAffected()
	return opened > 0, err
}

func (table problems) Clear(ctx context.Context, loopID, symptom string, at int64, by string) (bool, error) {
	result, err := table.db.ExecContext(ctx, `UPDATE loop_problems SET cleared_at=?, cleared_by=?
		WHERE loop_id=? AND symptom=? AND cleared_at = 0`, at, by, loopID, symptom)
	if err != nil {
		return false, err
	}
	cleared, err := result.RowsAffected()
	return cleared > 0, err
}

func (table problems) ListOpen(ctx context.Context, loopID string) ([]store.Problem, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT symptom, since FROM loop_problems
		WHERE loop_id=? AND cleared_at = 0 ORDER BY since, symptom`, loopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	open := []store.Problem{}
	for rows.Next() {
		problem := store.Problem{}
		if err := rows.Scan(&problem.Symptom, &problem.Since); err != nil {
			return nil, err
		}
		open = append(open, problem)
	}
	return open, rows.Err()
}
