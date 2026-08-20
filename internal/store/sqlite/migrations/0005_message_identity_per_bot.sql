-- A telegram message_id is numbered per bot conversation, not globally: two
-- bots see the same group message under different ids, and DMs to different
-- bots reuse the same id in the same chat (the chat id of a private chat is
-- the human's user id, shared by every bot). UNIQUE (tg_chat_id,
-- tg_message_id) therefore missed real duplicates in groups and invented
-- them in DMs, silently dropping messages. The receiving bot's loop becomes
-- part of a message's identity.
--
-- SQLite cannot alter a table constraint, so the table is rebuilt. Existing
-- rows keep an empty tg_bot_loop_id: which bot saw them was never recorded,
-- and they are already ingested.
CREATE TABLE messages_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts INTEGER NOT NULL,
    origin TEXT NOT NULL,
    author TEXT NOT NULL DEFAULT '',
    from_loop_id TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    mentions TEXT NOT NULL DEFAULT '[]',
    tg_chat_id INTEGER,
    tg_message_id INTEGER,
    tg_bot_loop_id TEXT NOT NULL DEFAULT '',
    delivered_to TEXT NOT NULL DEFAULT '[]',
    UNIQUE (tg_chat_id, tg_message_id, tg_bot_loop_id)
);
INSERT INTO messages_new
    (id, ts, origin, author, from_loop_id, text, mentions, tg_chat_id, tg_message_id, delivered_to)
SELECT id, ts, origin, author, from_loop_id, text, mentions, tg_chat_id, tg_message_id, delivered_to
FROM messages;
DROP TABLE messages;
ALTER TABLE messages_new RENAME TO messages;
CREATE INDEX idx_messages_ts ON messages(ts);

-- When a loop's bot bound to its group. A bot ingests only group messages
-- Telegram dated after its own bind, which is what makes the election in
-- ADR-0020 agree across pollers: a message dated after a committed bind was
-- received after it, so every poller's read sees the same candidate set.
-- Loops bound before this migration get 0 and stay eligible.
ALTER TABLE loops ADD COLUMN tg_group_bound_at INTEGER NOT NULL DEFAULT 0;
