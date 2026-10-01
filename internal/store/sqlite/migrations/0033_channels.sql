-- Channels (ADR-0038): conversations several loops and people share, each
-- with a name. The fleet channel is the first of them, named group, so every
-- row and every prompt that already says group keeps its meaning.
CREATE TABLE channels (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
INSERT INTO channels (name, created_at)
    VALUES ('group', CAST(strftime('%s', 'now') AS INTEGER) * 1000);

-- Membership of every channel but group, which is opt-in, so a row is a loop
-- the operator put there. group's stays where 0024 put it: every loop is in
-- it unless loops.outside_fleet_channel says otherwise, so no fleet loses a
-- member on upgrade.
CREATE TABLE channel_loops (
    channel  TEXT NOT NULL REFERENCES channels(name) ON DELETE CASCADE,
    loop_id  TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (channel, loop_id),
    CHECK (channel <> 'group')
);

-- A message in a channel names it; private conversations name none. Every
-- message already in the fleet channel is in group.
ALTER TABLE messages ADD COLUMN channel TEXT NOT NULL DEFAULT '';
UPDATE messages SET channel = 'group' WHERE conversation = 'group';
