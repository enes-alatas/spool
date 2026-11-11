-- A channel's chat (ADR-0070): the one Telegram group or Slack channel a
-- channel is bound to, once, for every member loop whose bot is in it. A
-- chat is one channel's at most, and a channel has one chat at most.
CREATE TABLE channel_chats (
    channel  TEXT PRIMARY KEY REFERENCES channels(name) ON DELETE CASCADE,
    surface  TEXT NOT NULL,
    chat_id  TEXT NOT NULL,
    -- the chat's name as a member's bot last heard it, '' for a chat bound
    -- by a pasted id that no member's bot has been in
    title    TEXT NOT NULL DEFAULT '',
    bound_at INTEGER NOT NULL,
    -- who bound it, as the audit names a user; 'upgrade' for this migration
    bound_by TEXT NOT NULL DEFAULT '',
    -- when a member's bot was first in the chat, 0 for none yet
    seen_at  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (surface, chat_id)
);

-- Each channel some room is bound to keeps the chat of its earliest-bound
-- room, so the loops already in it don't go quiet. The store refuses a
-- chat to a second channel, but rooms hold no such rule, so a hub's rooms
-- may carry one chat for two channels. That chat is kept for the channel
-- that bound it earliest, and the other channel keeps no chat.
CREATE TEMP TABLE channel_chat_kept AS
    SELECT channel, surface, room_id FROM (
        SELECT channel, surface, room_id, ROW_NUMBER() OVER (
            PARTITION BY surface, room_id ORDER BY bound_at, channel) AS chat_rank
        FROM (
            SELECT channel, surface, room_id, bound_at, ROW_NUMBER() OVER (
                PARTITION BY channel ORDER BY bound_at, loop_id, surface, room_id) AS rank
            FROM rooms WHERE channel <> ''
        ) WHERE rank = 1
    ) WHERE chat_rank = 1;

INSERT INTO channel_chats (channel, surface, chat_id, title, bound_at, bound_by, seen_at)
    SELECT kept.channel, kept.surface, kept.room_id,
        COALESCE((SELECT title FROM rooms WHERE surface = kept.surface AND room_id = kept.room_id
            AND channel = kept.channel AND title <> '' ORDER BY first_seen_at DESC LIMIT 1), ''),
        (SELECT MIN(bound_at) FROM rooms WHERE surface = kept.surface AND room_id = kept.room_id
            AND channel = kept.channel),
        'upgrade',
        COALESCE((SELECT MIN(first_seen_at) FROM rooms WHERE surface = kept.surface AND room_id = kept.room_id
            AND channel = kept.channel), 0)
    FROM channel_chat_kept AS kept;

-- A room bound to a channel in another chat than the one it kept, or to a
-- channel that kept none, unbinds, still recorded. A room that was
-- unbound stays unbound, even one for a chat kept. This is a plain table,
-- not a temporary one: the hub logs each channel it names at its next
-- start and then drops it, retrying at the start after if that fails
-- (sqlite.go).
CREATE TABLE channel_chat_upgrade_unbound AS
    SELECT rooms.channel, rooms.loop_id, rooms.surface, rooms.room_id
    FROM rooms WHERE rooms.channel <> '' AND NOT EXISTS (
        SELECT 1 FROM channel_chat_kept AS kept WHERE kept.channel = rooms.channel
            AND kept.surface = rooms.surface AND kept.room_id = rooms.room_id);

UPDATE rooms SET channel = '', bound_at = 0
    WHERE EXISTS (SELECT 1 FROM channel_chat_upgrade_unbound AS unbound
        WHERE unbound.loop_id = rooms.loop_id AND unbound.surface = rooms.surface
          AND unbound.room_id = rooms.room_id);

DROP TABLE channel_chat_kept;
