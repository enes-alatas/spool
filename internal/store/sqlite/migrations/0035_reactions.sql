-- Reactions (ADR-0040): one reactor's emoji on one hub message. A reactor is
-- a loop ('loop:<id>') or a person on a surface ('<surface>:<user id>'), so
-- every bot in a shared room may report the same reaction and the hub still
-- holds one row. told_at is when the loop that wrote the message was told,
-- 0 until then: the news rides with its next turn rather than waking it.
CREATE TABLE reactions (
    id          INTEGER PRIMARY KEY,
    message_id  INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    reactor_key TEXT NOT NULL,
    reactor     TEXT NOT NULL DEFAULT '',
    emoji       TEXT NOT NULL,
    ts          INTEGER NOT NULL,
    told_at     INTEGER NOT NULL DEFAULT 0,
    UNIQUE (message_id, reactor_key, emoji)
);
