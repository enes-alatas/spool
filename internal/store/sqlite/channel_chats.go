package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// A channel's chat (ADR-0070): the one chat a channel is bound to, which
// every member loop's bot in it carries the channel in. The helpers here
// take a querier so the writes that put a loop in a channel keep the
// chat's rules inside their own transaction.

// querier is a transaction or the database itself.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const chatCols = `channel, surface, chat_id, title, bound_at, bound_by, seen_at`

func chatOf(ctx context.Context, conn querier, channel string) (*store.ChannelChat, error) {
	var chat store.ChannelChat
	err := conn.QueryRowContext(ctx, `SELECT `+chatCols+` FROM channel_chats WHERE channel=?`, channel).
		Scan(&chat.Channel, &chat.Surface, &chat.ChatID, &chat.Title, &chat.BoundAt, &chat.BoundBy, &chat.SeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return &chat, err
}

// botColumn is the loops column a loop has a bot on surface by, "" for a
// surface no bot sits on.
func botColumn(surface string) string {
	switch surface {
	case store.SurfaceTelegram:
		return "tg_bot_token"
	case store.SurfaceSlack:
		return "slack_bot_token"
	}
	return ""
}

// marks is count comma-separated placeholders, for an IN list.
func marks(count int) string { return strings.TrimSuffix(strings.Repeat("?,", count), ",") }

func anys(values []string) []any {
	out := make([]any, len(values))
	for index, value := range values {
		out[index] = value
	}
	return out
}

// offSurface returns those of loopIDs that exist and have no bot on
// surface, sorted. A loop that is gone is no refusal of its own: the
// write it was named in answers ErrNotFound.
func offSurface(ctx context.Context, conn querier, surface string, loopIDs []string) ([]string, error) {
	if len(loopIDs) == 0 {
		return nil, nil
	}
	condition := "1"
	if column := botColumn(surface); column != "" {
		condition = column + " = ''"
	}
	return scanIDs(conn.QueryContext(ctx, `SELECT id FROM loops WHERE id IN (`+marks(len(loopIDs))+`) AND `+condition+` ORDER BY id`,
		anys(loopIDs)...))
}

func scanIDs(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// requireOnSurface refuses putting loopIDs among channel's members when
// the channel has a chat and one of them has no bot on its surface.
func requireOnSurface(ctx context.Context, conn querier, channel string, loopIDs []string) error {
	chat, err := chatOf(ctx, conn, channel)
	if errors.Is(err, store.ErrNotFound) {
		return nil // a channel with no chat takes any loop
	}
	if err != nil {
		return err
	}
	off, err := offSurface(ctx, conn, chat.Surface, loopIDs)
	if err != nil {
		return err
	}
	if len(off) > 0 {
		return &store.NotOnSurfaceError{Surface: chat.Surface, LoopIDs: off}
	}
	return nil
}

// notMembers returns those of loopIDs not in channel.
func notMembers(ctx context.Context, conn querier, channel string, loopIDs []string) ([]string, error) {
	var out []string
	for _, loopID := range loopIDs {
		var in int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_loops WHERE channel=? AND loop_id=?`,
			channel, loopID).Scan(&in); err != nil {
			return nil, err
		}
		if in == 0 {
			out = append(out, loopID)
		}
	}
	return out, nil
}

// bindJoined binds the room each of loopIDs, just put in channel, has for
// the channel's chat, if the channel has one and the loop's bot has a room
// for it (ADR-0070). A loop that already carries the channel in a
// room keeps it.
func bindJoined(ctx context.Context, conn querier, channel string, loopIDs []string, at int64) error {
	chat, err := chatOf(ctx, conn, channel)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, loopID := range loopIDs {
		if err := bindRoom(ctx, conn, chat, loopID, at); err != nil {
			return err
		}
	}
	return nil
}

// bindRoom binds loopID's room for chat to chat's channel, when it has an
// unbound one and carries the channel in no other room.
func bindRoom(ctx context.Context, conn querier, chat *store.ChannelChat, loopID string, at int64) error {
	_, err := conn.ExecContext(ctx, `UPDATE rooms SET channel=?1, bound_at=?2
		WHERE loop_id=?3 AND surface=?4 AND room_id=?5 AND channel=''
		AND NOT EXISTS (SELECT 1 FROM rooms AS carrying WHERE carrying.loop_id=?3 AND carrying.channel=?1)`,
		chat.Channel, at, loopID, chat.Surface, chat.ChatID)
	return err
}

// channelLoopIDs returns the loops in channel, sorted.
func channelLoopIDs(ctx context.Context, conn querier, channel string) ([]string, error) {
	return scanIDs(conn.QueryContext(ctx, `SELECT loop_id FROM channel_loops WHERE channel=? ORDER BY loop_id`, channel))
}

func (table channels) Chat(ctx context.Context, name string) (*store.ChannelChat, error) {
	return chatOf(ctx, table.db, name)
}

func (table channels) BindChat(ctx context.Context, chat *store.ChannelChat) (*store.ChannelChat, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := bindChat(ctx, tx, chat); err != nil {
		return nil, err
	}
	bound, err := chatOf(ctx, tx, chat.Channel)
	if err != nil {
		return nil, err
	}
	return bound, tx.Commit()
}

func (table channels) MoveChat(ctx context.Context, from string, chat *store.ChannelChat) (*store.ChannelChat, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := unbindChat(ctx, tx, from); err != nil {
		return nil, err
	}
	if err := bindChat(ctx, tx, chat); err != nil {
		return nil, err
	}
	bound, err := chatOf(ctx, tx, chat.Channel)
	if err != nil {
		return nil, err
	}
	return bound, tx.Commit()
}

// bindChat is BindChat inside conn's transaction.
func bindChat(ctx context.Context, conn querier, chat *store.ChannelChat) error {
	var channels int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels WHERE name=?`, chat.Channel).Scan(&channels); err != nil {
		return err
	}
	if channels == 0 {
		return store.ErrNotFound
	}
	if _, err := chatOf(ctx, conn, chat.Channel); err == nil {
		return store.ErrChannelHasChat
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var taken int
	if err := conn.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM channel_chats WHERE surface=?1 AND chat_id=?2)
		+ (SELECT COUNT(*) FROM rooms WHERE surface=?1 AND room_id=?2 AND channel<>'' AND channel<>?3)`,
		chat.Surface, chat.ChatID, chat.Channel).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return store.ErrRoomInUse
	}
	members, err := channelLoopIDs(ctx, conn, chat.Channel)
	if err != nil {
		return err
	}
	off, err := offSurface(ctx, conn, chat.Surface, members)
	if err != nil {
		return err
	}
	if len(off) > 0 {
		return &store.NotOnSurfaceError{Surface: chat.Surface, LoopIDs: off}
	}
	// the chat's name and first sighting, from the rooms the members'
	// bots have for it; a chat pasted on the channel's page has none
	inMembers := `loop_id IN (SELECT loop_id FROM channel_loops WHERE channel=?3)`
	var title string
	var seenAt int64
	if err := conn.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT title FROM rooms WHERE surface=?1 AND room_id=?2 AND title<>'' AND `+inMembers+`
			ORDER BY first_seen_at DESC LIMIT 1), ''),
		COALESCE((SELECT MIN(first_seen_at) FROM rooms WHERE surface=?1 AND room_id=?2 AND `+inMembers+`), 0)`,
		chat.Surface, chat.ChatID, chat.Channel).Scan(&title, &seenAt); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO channel_chats (`+chatCols+`) VALUES (?,?,?,?,?,?,?)`,
		chat.Channel, chat.Surface, chat.ChatID, title, chat.BoundAt, chat.BoundBy, seenAt); err != nil {
		return err
	}
	bound := *chat
	for _, loopID := range members {
		if err := bindRoom(ctx, conn, &bound, loopID, chat.BoundAt); err != nil {
			return err
		}
	}
	return nil
}

func (table channels) UnbindChat(ctx context.Context, name string) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := unbindChat(ctx, tx, name); err != nil {
		return err
	}
	return tx.Commit()
}

// unbindChat is UnbindChat inside conn's transaction. Every room bound to the
// channel is a room for its chat, so every one unbinds.
func unbindChat(ctx context.Context, conn querier, name string) error {
	res, err := conn.ExecContext(ctx, `DELETE FROM channel_chats WHERE channel=?`, name)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	_, err = conn.ExecContext(ctx, `UPDATE rooms SET channel='', bound_at=0 WHERE channel=?`, name)
	return err
}

func (table channels) BindMember(ctx context.Context, name, loopID string, at int64) error {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	chat, err := chatOf(ctx, tx, name)
	if err != nil {
		return err
	}
	var member, room int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM channel_loops WHERE channel=?1 AND loop_id=?2),
		(SELECT COUNT(*) FROM rooms WHERE loop_id=?2 AND surface=?3 AND room_id=?4)`,
		name, loopID, chat.Surface, chat.ChatID).Scan(&member, &room); err != nil {
		return err
	}
	switch {
	case member == 0:
		return store.ErrNotInChannel
	case room == 0:
		return store.ErrNotInChat
	}
	if err := bindRoom(ctx, tx, chat, loopID, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (table channels) FoundChats(ctx context.Context, name string) ([]*store.FoundChat, error) {
	members, err := channelLoopIDs(ctx, table.db, name)
	if err != nil || len(members) == 0 {
		return []*store.FoundChat{}, err
	}
	rows, err := table.db.QueryContext(ctx, `SELECT surface, room_id, loop_id, title, first_seen_at FROM rooms
		WHERE loop_id IN (`+marks(len(members))+`) AND channel=''
		AND NOT EXISTS (SELECT 1 FROM channel_chats WHERE channel_chats.surface=rooms.surface AND channel_chats.chat_id=rooms.room_id)
		AND NOT EXISTS (SELECT 1 FROM rooms AS bound WHERE bound.surface=rooms.surface AND bound.room_id=rooms.room_id AND bound.channel<>'')
		ORDER BY first_seen_at, surface, room_id`, anys(members)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := []*store.FoundChat{}
	byChat := map[string]*store.FoundChat{}
	for rows.Next() {
		var surface, roomID, loopID, title string
		var firstSeen int64
		if err := rows.Scan(&surface, &roomID, &loopID, &title, &firstSeen); err != nil {
			return nil, err
		}
		key := surface + "\x00" + roomID
		chat := byChat[key]
		if chat == nil {
			chat = &store.FoundChat{Surface: surface, ChatID: roomID, FirstSeenAt: firstSeen}
			byChat[key] = chat
			found = append(found, chat)
		}
		if title != "" {
			chat.Title = title
		}
		chat.FoundBy = append(chat.FoundBy, loopID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// a chat on a surface some member has no bot on can't be the
	// channel's (ADR-0070)
	offered := found[:0]
	offBySurface := map[string]bool{}
	for _, chat := range found {
		off, seen := offBySurface[chat.Surface]
		if !seen {
			missing, err := offSurface(ctx, table.db, chat.Surface, members)
			if err != nil {
				return nil, err
			}
			off = len(missing) > 0
			offBySurface[chat.Surface] = off
		}
		if !off {
			sort.Strings(chat.FoundBy)
			offered = append(offered, chat)
		}
	}
	return offered, nil
}

// sightChat keeps a channel's chat up to date with a member's sighting of
// it: the title its bot heard, and when a member's bot was first in it. A
// room recorded just now for a channel's chat, by a member, is bound to the
// channel at once (ADR-0070).
func sightChat(ctx context.Context, conn querier, room *store.Room, added bool) error {
	var channel string
	err := conn.QueryRowContext(ctx, `SELECT channel_chats.channel FROM channel_chats
		JOIN channel_loops ON channel_loops.channel = channel_chats.channel AND channel_loops.loop_id = ?
		WHERE channel_chats.surface=? AND channel_chats.chat_id=?`, room.LoopID, room.Surface, room.RoomID).Scan(&channel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // no channel's chat, or not this loop's channel
	}
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `UPDATE channel_chats SET
		title = CASE WHEN ?1 <> '' THEN ?1 ELSE title END,
		seen_at = CASE WHEN seen_at = 0 THEN ?2 ELSE seen_at END
		WHERE channel=?3`, room.Title, room.FirstSeenAt, channel); err != nil {
		return err
	}
	if !added {
		return nil
	}
	chat, err := chatOf(ctx, conn, channel)
	if err != nil {
		return err
	}
	return bindRoom(ctx, conn, chat, room.LoopID, room.FirstSeenAt)
}
