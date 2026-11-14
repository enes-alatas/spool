package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/enes-alatas/spool/internal/store"
)

// The hub's one order of its teams (#1451). Each team keeps its place in
// teams.position, so a rename keeps it and a delete takes it away; No team's
// place is the setting below, on the same scale. A team sits before No team
// when its position is lower. The setting is absent until somebody sets an
// order, and then No team is last.
const noTeamPlaceKey = "team_order_no_team"

// noTeamPlace reads No team's place, and whether one is set.
func noTeamPlace(ctx context.Context, db querier) (int, bool, error) {
	var value string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, noTeamPlaceKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	place, err := strconv.Atoi(value)
	return place, err == nil, err
}

func setNoTeamPlace(ctx context.Context, db querier, place int) error {
	_, err := db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, noTeamPlaceKey, strconv.Itoa(place))
	return err
}

// takeLastTeamPlace is the place a new team takes: last, but before No team
// when No team is last, which moves No team one on.
func takeLastTeamPlace(ctx context.Context, tx querier) (int, error) {
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) FROM teams`).Scan(&last); err != nil {
		return 0, err
	}
	noTeam, set, err := noTeamPlace(ctx, tx)
	if err != nil || !set || noTeam <= last {
		return last + 1, err
	}
	return noTeam, setNoTeamPlace(ctx, tx, noTeam+1)
}

func (table teams) Order(ctx context.Context) ([]string, error) {
	noTeam, set, err := noTeamPlace(ctx, table.db)
	if err != nil {
		return nil, err
	}
	rows, err := table.db.QueryContext(ctx, `SELECT name, position FROM teams ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	order, placed := []string{}, false
	for rows.Next() {
		var name string
		var position int
		if err := rows.Scan(&name, &position); err != nil {
			return nil, err
		}
		if set && !placed && position >= noTeam {
			order, placed = append(order, ""), true
		}
		order = append(order, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !placed {
		order = append(order, "")
	}
	return order, nil
}

func (table teams) SetOrder(ctx context.Context, order []string) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams`).Scan(&count); err != nil {
		return err
	}
	// every team once and No team once is exactly count+1 distinct names,
	// each of them No team or a team the update below finds
	if len(order) != count+1 {
		return store.ErrTeamOrderStale
	}
	seen := map[string]bool{}
	for place, name := range order {
		if seen[name] {
			return store.ErrTeamOrderStale
		}
		seen[name] = true
		if name == "" {
			if err := setNoTeamPlace(ctx, tx, place); err != nil {
				return err
			}
			continue
		}
		res, err := tx.ExecContext(ctx, `UPDATE teams SET position=? WHERE name=?`, place, name)
		if err != nil {
			return err
		}
		if updated, err := res.RowsAffected(); err != nil || updated == 0 {
			return errors.Join(err, store.ErrTeamOrderStale)
		}
	}
	if !seen[""] {
		return store.ErrTeamOrderStale
	}
	return tx.Commit()
}
