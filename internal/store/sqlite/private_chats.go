package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/enes-alatas/spool/internal/store"
)

type privateChats struct{ db *sql.DB }

func (table privateChats) Capture(ctx context.Context, chat *store.PrivateChat) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO private_chats (loop_id, person, chat, captured_at)
		VALUES (?,?,?,?)
		ON CONFLICT (loop_id, person) DO UPDATE SET chat=excluded.chat, captured_at=excluded.captured_at
		WHERE chat != excluded.chat`,
		chat.LoopID, chat.Person, chat.Chat, chat.CapturedAt)
	return err
}

func (table privateChats) Get(ctx context.Context, loopID, person string) (*store.PrivateChat, error) {
	chat := store.PrivateChat{LoopID: loopID, Person: person}
	err := table.db.QueryRowContext(ctx, `SELECT chat, captured_at FROM private_chats WHERE loop_id=? AND person=?`,
		loopID, person).Scan(&chat.Chat, &chat.CapturedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &chat, nil
}

func (table privateChats) List(ctx context.Context, loopID string) ([]*store.PrivateChat, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT person, chat, captured_at FROM private_chats
		WHERE loop_id=? ORDER BY captured_at, person`, loopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chats := []*store.PrivateChat{}
	for rows.Next() {
		chat := store.PrivateChat{LoopID: loopID}
		if err := rows.Scan(&chat.Person, &chat.Chat, &chat.CapturedAt); err != nil {
			return nil, err
		}
		chats = append(chats, &chat)
	}
	return chats, rows.Err()
}
