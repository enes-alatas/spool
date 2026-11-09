package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// teams are the hub's teams (ADR-0066). A loop's team is its own loops.team
// column, which follows a rename and keeps a team with loops from being
// deleted, so the loops in a team are read from there.
type teams struct{ db *sql.DB }

func (table teams) List(ctx context.Context) ([]*store.Team, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT name, description, created_at FROM teams ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var out []*store.Team
	for rows.Next() {
		var team store.Team
		if err := rows.Scan(&team.Name, &team.Description, &team.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, &team)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, team := range out {
		if team.LoopIDs, err = table.loopIDs(ctx, team.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (table teams) Get(ctx context.Context, name string) (*store.Team, error) {
	var team store.Team
	err := table.db.QueryRowContext(ctx, `SELECT name, description, created_at FROM teams WHERE name=?`, name).
		Scan(&team.Name, &team.Description, &team.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if team.LoopIDs, err = table.loopIDs(ctx, name); err != nil {
		return nil, err
	}
	return &team, nil
}

func (table teams) loopIDs(ctx context.Context, name string) ([]string, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT id FROM loops WHERE team=? ORDER BY id`, name)
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

func (table teams) Create(ctx context.Context, team *store.Team) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO teams (name, description, created_at) VALUES (?,?,?)`,
		team.Name, team.Description, team.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	team.LoopIDs = []string{}
	return err
}

func (table teams) Edit(ctx context.Context, name string, edit store.TeamEdit) (*store.Team, error) {
	var sets []string
	var args []any
	if edit.Name != nil {
		sets, args = append(sets, "name=?"), append(args, *edit.Name)
	}
	if edit.Description != nil {
		sets, args = append(sets, "description=?"), append(args, *edit.Description)
	}
	if len(sets) == 0 {
		return table.Get(ctx, name)
	}
	// loops.team follows the rename by ON UPDATE CASCADE
	res, err := table.db.ExecContext(ctx, `UPDATE teams SET `+strings.Join(sets, ", ")+` WHERE name=?`, append(args, name)...)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return nil, store.ErrDuplicate
	}
	if err != nil {
		return nil, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, store.ErrNotFound
	}
	if edit.Name != nil {
		name = *edit.Name
	}
	return table.Get(ctx, name)
}

func (table teams) Delete(ctx context.Context, name string) error {
	res, err := table.db.ExecContext(ctx, `DELETE FROM teams WHERE name=?`, name)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return store.ErrTeamHasLoops
	}
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return nil
}
