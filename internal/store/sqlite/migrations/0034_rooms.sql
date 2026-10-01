-- Rooms (ADR-0038 item 5): a surface's chat as one loop's bot knows it, bound
-- to one of the loop's channels or to none yet. The hub records a room at its
-- first message, so the operator can bind it from the loop page; an unbound
-- room carries nothing either way.
CREATE TABLE rooms (
    loop_id       TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    surface       TEXT NOT NULL,
    room_id       TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    channel       TEXT NOT NULL DEFAULT '',
    first_seen_at INTEGER NOT NULL,
    bound_at      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (loop_id, surface, room_id)
);
-- One room per channel per loop; any number unbound.
CREATE UNIQUE INDEX rooms_loop_channel ON rooms (loop_id, channel) WHERE channel <> '';
-- Every loop's binding of one room, which is what the ingest election reads.
CREATE INDEX rooms_room ON rooms (surface, room_id);

-- The fleet channel's Telegram group becomes its room, bound when it was.
INSERT INTO rooms (loop_id, surface, room_id, channel, first_seen_at, bound_at)
    SELECT id, 'telegram', CAST(tg_group_chat_id AS TEXT), 'group', tg_group_bound_at, tg_group_bound_at
    FROM loops WHERE tg_group_chat_id <> 0;
ALTER TABLE loops DROP COLUMN tg_group_chat_id;
ALTER TABLE loops DROP COLUMN tg_group_bound_at;
