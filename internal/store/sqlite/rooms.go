package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// rooms is every loop's view of the surface chats its bot is in (ADR-0038).
// The fleet channel's room is a row like any other, bound to group.
type rooms struct{ db *sql.DB }

const roomCols = `loop_id, surface, room_id, title, channel, first_seen_at, bound_at`

func scanRooms(rows *sql.Rows, err error) ([]*store.Room, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.Room{}
	for rows.Next() {
		var room store.Room
		if err := rows.Scan(&room.LoopID, &room.Surface, &room.RoomID, &room.Title, &room.Channel,
			&room.FirstSeenAt, &room.BoundAt); err != nil {
			return nil, err
		}
		out = append(out, &room)
	}
	return out, rows.Err()
}

func (table rooms) List(ctx context.Context, loopID string) ([]*store.Room, error) {
	return scanRooms(table.db.QueryContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE loop_id=?
		ORDER BY first_seen_at DESC, room_id`, loopID))
}

func (table rooms) ListByRoom(ctx context.Context, surface, roomID string) ([]*store.Room, error) {
	return scanRooms(table.db.QueryContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE surface=? AND room_id=?
		ORDER BY loop_id`, surface, roomID))
}

func (table rooms) get(ctx context.Context, querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, loopID, surface, roomID string) (*store.Room, error) {
	var room store.Room
	err := querier.QueryRowContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE loop_id=? AND surface=? AND room_id=?`,
		loopID, surface, roomID).Scan(&room.LoopID, &room.Surface, &room.RoomID, &room.Title, &room.Channel,
		&room.FirstSeenAt, &room.BoundAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return &room, err
}

func (table rooms) Sight(ctx context.Context, room *store.Room) (*store.Room, bool, error) {
	res, err := table.db.ExecContext(ctx, `INSERT INTO rooms (loop_id, surface, room_id, title, first_seen_at)
		VALUES (?,?,?,?,?) ON CONFLICT (loop_id, surface, room_id) DO NOTHING`,
		room.LoopID, room.Surface, room.RoomID, room.Title, room.FirstSeenAt)
	if err != nil {
		return nil, false, err
	}
	added, _ := res.RowsAffected()
	if added == 0 && room.Title != "" {
		// a title is refreshed, never blanked: a message that names no
		// title says nothing about the one the room has
		if _, err := table.db.ExecContext(ctx, `UPDATE rooms SET title=? WHERE loop_id=? AND surface=? AND room_id=?`,
			room.Title, room.LoopID, room.Surface, room.RoomID); err != nil {
			return nil, false, err
		}
	}
	stored, err := table.get(ctx, table.db, room.LoopID, room.Surface, room.RoomID)
	return stored, added > 0, err
}

func (table rooms) Bind(ctx context.Context, loopID, surface, roomID, channel string, at int64) (*store.Room, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var other int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE surface=? AND room_id=?
		AND loop_id<>? AND channel<>'' AND channel<>?`, surface, roomID, loopID, channel).Scan(&other); err != nil {
		return nil, err
	}
	if other > 0 {
		return nil, store.ErrRoomInUse
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET channel='', bound_at=0
		WHERE loop_id=? AND channel=? AND NOT (surface=? AND room_id=?)`, loopID, channel, surface, roomID); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rooms (loop_id, surface, room_id, channel, first_seen_at, bound_at)
		VALUES (?,?,?,?,?,?) ON CONFLICT (loop_id, surface, room_id) DO UPDATE SET
		bound_at = CASE WHEN rooms.channel = excluded.channel THEN rooms.bound_at ELSE excluded.bound_at END,
		channel = excluded.channel`, loopID, surface, roomID, channel, at, at)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	room, err := table.get(ctx, tx, loopID, surface, roomID)
	if err != nil {
		return nil, err
	}
	return room, tx.Commit()
}

func (table rooms) Forget(ctx context.Context, loopID, surface, roomID string) error {
	res, err := table.db.ExecContext(ctx, `DELETE FROM rooms WHERE loop_id=? AND surface=? AND room_id=?`,
		loopID, surface, roomID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (table rooms) Move(ctx context.Context, surface, fromRoomID, toRoomID string) ([]string, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var clash int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms AS moving JOIN rooms AS there
		ON there.surface=moving.surface AND there.room_id=? AND there.channel<>'' AND there.channel<>moving.channel
		WHERE moving.surface=? AND moving.room_id=? AND moving.channel<>''`,
		toRoomID, surface, fromRoomID).Scan(&clash); err != nil {
		return nil, err
	}
	if clash > 0 {
		return nil, store.ErrRoomInUse
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM rooms WHERE surface=? AND room_id=? AND channel=''
		AND loop_id IN (SELECT loop_id FROM rooms WHERE surface=? AND room_id=?)`,
		surface, toRoomID, surface, fromRoomID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `UPDATE rooms SET room_id=? WHERE surface=? AND room_id=?
		AND loop_id NOT IN (SELECT loop_id FROM rooms WHERE surface=? AND room_id=?) RETURNING loop_id`,
		toRoomID, surface, fromRoomID, surface, toRoomID)
	if err != nil {
		return nil, err
	}
	moved := []string{}
	for rows.Next() {
		var loopID string
		if err := rows.Scan(&loopID); err != nil {
			rows.Close()
			return nil, err
		}
		moved = append(moved, loopID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(moved)
	return moved, tx.Commit()
}
