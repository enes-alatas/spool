-- The fleet channel becomes an ordinary channel (ADR-0068). The
-- channel named group keeps its members, history and bound rooms under the
-- name fleet, or the first free fleet-N when a channel already has that
-- name, and is in no team. A group with no member, no message and no bound
-- room, as on a fresh install, is dropped instead, so a new hub has no
-- fleet-wide channel (item 7). Either way group is gone, and stays a
-- reserved name: channel_loops already refuses a row for it.
CREATE TEMP TABLE fleet_rename (name TEXT NOT NULL);

INSERT INTO fleet_rename (name)
    WITH RECURSIVE candidate(n) AS (
        SELECT 1 UNION ALL SELECT n + 1 FROM candidate WHERE n < 1000
    )
    SELECT CASE n WHEN 1 THEN 'fleet' ELSE 'fleet-' || n END
    FROM candidate
    WHERE CASE n WHEN 1 THEN 'fleet' ELSE 'fleet-' || n END
        NOT IN (SELECT name FROM channels)
      AND (EXISTS (SELECT 1 FROM loops WHERE outside_fleet_channel = 0)
        OR EXISTS (SELECT 1 FROM messages WHERE channel = 'group')
        OR EXISTS (SELECT 1 FROM rooms WHERE channel = 'group'))
    ORDER BY n
    LIMIT 1;

INSERT INTO channels (name, description, created_at)
    SELECT fleet_rename.name, channels.description, channels.created_at
    FROM fleet_rename, channels WHERE channels.name = 'group';

-- every loop that was in group, the exception column read the right way
-- round, joins as an ordinary member from when it was made
INSERT INTO channel_loops (channel, loop_id, added_at)
    SELECT fleet_rename.name, loops.id, loops.created_at
    FROM fleet_rename, loops WHERE loops.outside_fleet_channel = 0;

UPDATE messages SET channel = (SELECT name FROM fleet_rename)
    WHERE channel = 'group' AND EXISTS (SELECT 1 FROM fleet_rename);
UPDATE rooms SET channel = (SELECT name FROM fleet_rename)
    WHERE channel = 'group' AND EXISTS (SELECT 1 FROM fleet_rename);

DELETE FROM channels WHERE name = 'group';
DROP TABLE fleet_rename;

-- membership is rows for every channel now (ADR-0068, ADR-0038 item 3)
ALTER TABLE loops DROP COLUMN outside_fleet_channel;
