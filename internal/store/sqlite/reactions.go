package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// reactions is every reactor's emoji on the hub's messages (ADR-0040).
type reactions struct{ db *sql.DB }

const reactionCols = `r.id, r.message_id, r.reactor_key, r.reactor, r.emoji, r.ts, r.told_at`

func scanReactions(rows *sql.Rows, err error) ([]*store.Reaction, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.Reaction{}
	for rows.Next() {
		var reaction store.Reaction
		if err := rows.Scan(&reaction.ID, &reaction.MessageID, &reaction.ReactorKey, &reaction.Reactor,
			&reaction.Emoji, &reaction.TS, &reaction.ToldAt); err != nil {
			return nil, err
		}
		out = append(out, &reaction)
	}
	return out, rows.Err()
}

func (table reactions) Add(ctx context.Context, reaction *store.Reaction) (bool, error) {
	res, err := table.db.ExecContext(ctx, `INSERT INTO reactions (message_id, reactor_key, reactor, emoji, ts)
		VALUES (?,?,?,?,?) ON CONFLICT (message_id, reactor_key, emoji) DO NOTHING`,
		reaction.MessageID, reaction.ReactorKey, reaction.Reactor, reaction.Emoji, reaction.TS)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return false, store.ErrNotFound
		}
		return false, err
	}
	added, err := res.RowsAffected()
	if err != nil || added == 0 {
		return false, err
	}
	reaction.ID, err = res.LastInsertId()
	return true, err
}

func (table reactions) Remove(ctx context.Context, messageID int64, reactorKey, emoji string) (bool, error) {
	res, err := table.db.ExecContext(ctx, `DELETE FROM reactions WHERE message_id=? AND reactor_key=? AND emoji=?`,
		messageID, reactorKey, emoji)
	if err != nil {
		return false, err
	}
	removed, err := res.RowsAffected()
	return removed > 0, err
}

func (table reactions) ListByMessages(ctx context.Context, messageIDs []int64) ([]*store.Reaction, error) {
	if len(messageIDs) == 0 {
		return []*store.Reaction{}, nil
	}
	args := make([]any, 0, len(messageIDs))
	for _, id := range messageIDs {
		args = append(args, id)
	}
	return scanReactions(table.db.QueryContext(ctx, `SELECT `+reactionCols+` FROM reactions AS r
		WHERE r.message_id IN (`+strings.Repeat(",?", len(messageIDs))[1:]+`) ORDER BY r.ts, r.id`, args...))
}

func (table reactions) Untold(ctx context.Context, loopID string) ([]*store.Reaction, error) {
	return scanReactions(table.db.QueryContext(ctx, `SELECT `+reactionCols+` FROM reactions AS r
		JOIN messages AS m ON m.id = r.message_id
		WHERE m.from_loop_id=? AND r.reactor_key<>? AND r.told_at=0 ORDER BY r.ts, r.id`,
		loopID, store.LoopReactor(loopID)))
}

func (table reactions) MarkTold(ctx context.Context, ids []int64, toldAt int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, toldAt)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := table.db.ExecContext(ctx,
		`UPDATE reactions SET told_at=? WHERE id IN (`+strings.Repeat(",?", len(ids))[1:]+`)`, args...)
	return err
}
