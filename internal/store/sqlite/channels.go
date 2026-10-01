package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// channels keeps two shapes of membership behind one interface (ADR-0038).
// The fleet channel holds every loop unless the operator took it out, so its
// membership is the loops' outside_fleet_channel exception (0024); every
// other channel is opt-in, so its membership is a channel_loops row per loop
// put there.
type channels struct{ db *sql.DB }

func (table channels) List(ctx context.Context) ([]*store.Channel, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT name, description, created_at FROM channels
		ORDER BY name <> ?, name`, store.FleetChannel)
	if err != nil {
		return nil, err
	}
	var out []*store.Channel
	for rows.Next() {
		var channel store.Channel
		if err := rows.Scan(&channel.Name, &channel.Description, &channel.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, &channel)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, channel := range out {
		if channel.LoopIDs, err = table.loopIDs(ctx, channel.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (table channels) Get(ctx context.Context, name string) (*store.Channel, error) {
	var channel store.Channel
	err := table.db.QueryRowContext(ctx, `SELECT name, description, created_at FROM channels WHERE name=?`, name).
		Scan(&channel.Name, &channel.Description, &channel.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if channel.LoopIDs, err = table.loopIDs(ctx, name); err != nil {
		return nil, err
	}
	return &channel, nil
}

// loopIDs is the membership of one channel, in whichever shape it is kept.
func (table channels) loopIDs(ctx context.Context, name string) ([]string, error) {
	statement, args := `SELECT loop_id FROM channel_loops WHERE channel=? ORDER BY loop_id`, []any{name}
	if name == store.FleetChannel {
		statement, args = `SELECT id FROM loops WHERE outside_fleet_channel=0 ORDER BY id`, nil
	}
	rows, err := table.db.QueryContext(ctx, statement, args...)
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

func (table channels) Create(ctx context.Context, channel *store.Channel) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO channels (name, description, created_at) VALUES (?,?,?)`,
		channel.Name, channel.Description, channel.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	channel.LoopIDs = []string{}
	return err
}

func (table channels) SetDescription(ctx context.Context, name, description string) (*store.Channel, error) {
	res, err := table.db.ExecContext(ctx, `UPDATE channels SET description=? WHERE name=?`, description, name)
	if err != nil {
		return nil, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, store.ErrNotFound
	}
	return table.Get(ctx, name)
}

func (table channels) Delete(ctx context.Context, name string) error {
	res, err := table.db.ExecContext(ctx, `DELETE FROM channels WHERE name=?`, name)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (table channels) AddLoop(ctx context.Context, name, loopID string, at int64) error {
	return table.setMember(ctx, name, loopID, true, at)
}

func (table channels) RemoveLoop(ctx context.Context, name, loopID string, at int64) error {
	return table.setMember(ctx, name, loopID, false, at)
}

func (table channels) setMember(ctx context.Context, name, loopID string, in bool, at int64) error {
	if name == store.FleetChannel {
		// The same write as a loop edit's OutsideFleetChannel, stamped the
		// same way, so the loop reads as changed whichever route moved it.
		res, err := table.db.ExecContext(ctx, `UPDATE loops SET outside_fleet_channel=?, updated_at=? WHERE id=?`, !in, at, loopID)
		if err != nil {
			return err
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			return store.ErrNotFound
		}
		return nil
	}
	if _, err := table.Get(ctx, name); err != nil {
		return err
	}
	if in {
		_, err := table.db.ExecContext(ctx, `INSERT INTO channel_loops (channel, loop_id, added_at) VALUES (?,?,?)
			ON CONFLICT (channel, loop_id) DO NOTHING`, name, loopID, at)
		if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
			return store.ErrNotFound
		}
		return err
	}
	_, err := table.db.ExecContext(ctx, `DELETE FROM channel_loops WHERE channel=? AND loop_id=?`, name, loopID)
	return err
}
