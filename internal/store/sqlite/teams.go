package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// teams are the hub's teams (ADR-0066). A loop's team is its own loops.team
// column and a channel's its channels.team, both of which follow a rename
// and keep a team that still has loops from being deleted, so the loops and
// channels of a team are read from there.
type teams struct{ db *sql.DB }

const teamCols = `name, description, created_at, COALESCE(home_channel, ''), COALESCE(claude_login, '')`

func scanTeam(row interface{ Scan(...any) error }) (*store.Team, error) {
	var team store.Team
	if err := row.Scan(&team.Name, &team.Description, &team.CreatedAt, &team.HomeChannel, &team.ClaudeLogin); err != nil {
		return nil, err
	}
	return &team, nil
}

func (table teams) List(ctx context.Context) ([]*store.Team, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+teamCols+` FROM teams ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var out []*store.Team
	for rows.Next() {
		team, err := scanTeam(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, team)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, team := range out {
		if err := table.readMembers(ctx, team); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (table teams) Get(ctx context.Context, name string) (*store.Team, error) {
	team, err := scanTeam(table.db.QueryRowContext(ctx, `SELECT `+teamCols+` FROM teams WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := table.readMembers(ctx, team); err != nil {
		return nil, err
	}
	return team, nil
}

// readMembers fills in the team's loops and channels.
func (table teams) readMembers(ctx context.Context, team *store.Team) error {
	var err error
	if team.LoopIDs, err = table.names(ctx, `SELECT id FROM loops WHERE team=? ORDER BY id`, team.Name); err != nil {
		return err
	}
	team.Channels, err = table.names(ctx, `SELECT name FROM channels WHERE team=? ORDER BY name`, team.Name)
	return err
}

func (table teams) names(ctx context.Context, query, team string) ([]string, error) {
	rows, err := table.db.QueryContext(ctx, query, team)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (table teams) Create(ctx context.Context, team *store.Team) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO teams (name, description, created_at, claude_login) VALUES (?,?,?,NULLIF(?, ''))`,
		team.Name, team.Description, team.CreatedAt, team.ClaudeLogin)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return store.ErrNotFound // no Claude login by that name
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	if err != nil {
		return err
	}
	team.LoopIDs, team.Channels = []string{}, []string{}
	if team.HomeChannel != "" {
		_, err := tx.ExecContext(ctx, `INSERT INTO channels (name, created_at, team) VALUES (?,?,?)`,
			team.HomeChannel, team.CreatedAt, team.Name)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return store.ErrChannelTaken
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE teams SET home_channel=? WHERE name=?`, team.HomeChannel, team.Name); err != nil {
			return err
		}
		team.Channels = []string{team.HomeChannel}
	}
	return tx.Commit()
}

func (table teams) Edit(ctx context.Context, name string, edit store.TeamEdit) (*store.Team, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var home string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(home_channel, '') FROM teams WHERE name=?`, name).Scan(&home)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	if edit.Name != nil {
		sets, args = append(sets, "name=?"), append(args, *edit.Name)
	}
	if edit.Description != nil {
		sets, args = append(sets, "description=?"), append(args, *edit.Description)
	}
	if edit.ClaudeLogin != nil {
		sets, args = append(sets, "claude_login=NULLIF(?, '')"), append(args, *edit.ClaudeLogin)
	}
	if len(sets) > 0 {
		// loops.team and channels.team follow a rename by ON UPDATE CASCADE
		_, err := tx.ExecContext(ctx, `UPDATE teams SET `+strings.Join(sets, ", ")+` WHERE name=?`, append(args, name)...)
		if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
			return nil, store.ErrNotFound // no Claude login by that name
		}
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return nil, store.ErrDuplicate
		}
		if err != nil {
			return nil, err
		}
	}
	if edit.Name != nil {
		name = *edit.Name
	}
	// Naming the home it already has changes nothing, so a loop the
	// operator took out of it stays out.
	if edit.HomeChannel != nil && *edit.HomeChannel != home {
		if err := setHome(ctx, tx, name, *edit.HomeChannel, edit.At); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return table.Get(ctx, name)
}

// setHome makes a channel the team's home and puts every loop of the team
// in it, or with "" leaves the team with none. A hub channel is given to
// the team in the same write (ADR-0068), as a channel's own move gives it:
// refused while it holds a loop outside the team. Another team's channel
// is refused.
func setHome(ctx context.Context, tx *sql.Tx, team, channel string, at int64) error {
	if channel == "" {
		_, err := tx.ExecContext(ctx, `UPDATE teams SET home_channel=NULL WHERE name=?`, team)
		return err
	}
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(team, '') FROM channels WHERE name=?`, channel).Scan(&owner)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return store.ErrChannelNotInTeam
	case err != nil:
		return err
	case owner == "":
		var outsiders int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_loops JOIN loops ON loops.id = channel_loops.loop_id
			WHERE channel_loops.channel=? AND COALESCE(loops.team, '') <> ?`, channel, team).Scan(&outsiders); err != nil {
			return err
		}
		if outsiders > 0 {
			return store.ErrChannelHasOutsiders
		}
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET team=? WHERE name=?`, team, channel); err != nil {
			return err
		}
	case owner != team:
		return store.ErrChannelInOtherTeam
	}

	if _, err := tx.ExecContext(ctx, `UPDATE teams SET home_channel=? WHERE name=?`, channel, team); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_loops (channel, loop_id, added_at)
		SELECT ?, id, ? FROM loops WHERE team=? ON CONFLICT (channel, loop_id) DO NOTHING`, channel, at, team)
	return err
}

func (table teams) Delete(ctx context.Context, name string) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists, loops int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), (SELECT COUNT(*) FROM loops WHERE team=?1) FROM teams WHERE name=?1`, name).
		Scan(&exists, &loops); err != nil {
		return err
	}
	switch {
	case exists == 0:
		return store.ErrNotFound
	case loops > 0:
		return store.ErrTeamHasLoops
	}
	// Its channels go as a deleted channel does: membership with them,
	// rooms unbound, messages keeping the name.
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET channel='', bound_at=0
		WHERE channel IN (SELECT name FROM channels WHERE team=?)`, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE team=?`, name); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM teams WHERE name=?`, name)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return store.ErrTeamHasLoops // a loop joined it meanwhile
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// execer is a transaction or the database itself.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// moveTeamChannels follows a loop from one team to another: out of every
// channel of the team it left, its rooms there unbound as leaving a channel
// does, and into the home channel of the team it joined, if it has one
// (ADR-0066). "" is no team on either side.
func moveTeamChannels(ctx context.Context, tx execer, loopID, left, joined string, at int64) error {
	if left != "" {
		const leftChannels = `SELECT name FROM channels WHERE team=?`
		if _, err := tx.ExecContext(ctx, `UPDATE rooms SET channel='', bound_at=0
			WHERE loop_id=? AND channel IN (`+leftChannels+`)`, loopID, left); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM channel_loops
			WHERE loop_id=? AND channel IN (`+leftChannels+`)`, loopID, left); err != nil {
			return err
		}
	}
	if joined != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_loops (channel, loop_id, added_at)
			SELECT home_channel, ?, ? FROM teams WHERE name=? AND home_channel IS NOT NULL
			ON CONFLICT (channel, loop_id) DO NOTHING`, loopID, at, joined); err != nil {
			return err
		}
	}
	return nil
}
