-- The record of every change to a connection (ADR-0043, #606): append-only,
-- one row per change that changed something. It names the connection and
-- the loop rather than referencing them, so a row outlives both, and keeps
-- the loop's name as it was. loop_id and loop_name are '' for a change no
-- loop is part of.
CREATE TABLE connection_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    action     TEXT NOT NULL,
    connection TEXT NOT NULL,
    loop_id    TEXT NOT NULL DEFAULT '',
    loop_name  TEXT NOT NULL DEFAULT '',
    at         INTEGER NOT NULL
);
CREATE INDEX idx_connection_events_connection ON connection_events(connection);
CREATE INDEX idx_connection_events_loop ON connection_events(loop_id);
CREATE INDEX idx_connection_events_loop_name ON connection_events(loop_name);

-- What is stored today is all the record can start from: each connection's
-- creation and each attachment still in place, in one insert ordered by
-- time, because the record reads in the order its rows were written. A
-- value replaced or a loop detached before now left nothing to read back.
INSERT INTO connection_events (action, connection, loop_id, loop_name, at)
SELECT action, connection, loop_id, loop_name, at FROM (
    SELECT 'create' AS action, name AS connection, '' AS loop_id, '' AS loop_name, created_at AS at, 0 AS step
    FROM connections
    UNION ALL
    SELECT 'attach', connection_loops.connection, connection_loops.loop_id, loops.name, connection_loops.attached_at, 1
    FROM connection_loops JOIN loops ON loops.id = connection_loops.loop_id
)
ORDER BY at, step, connection, loop_id;
