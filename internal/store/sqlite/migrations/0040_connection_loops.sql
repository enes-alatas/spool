-- Which loops each connection is attached to (ADR-0043). A loop's
-- attachments go with it; a connection is refused deletion while it has
-- any, so its own cascade is only a backstop.
CREATE TABLE connection_loops (
    connection  TEXT NOT NULL REFERENCES connections(name) ON DELETE CASCADE,
    loop_id     TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    attached_at INTEGER NOT NULL,
    PRIMARY KEY (connection, loop_id)
);
CREATE INDEX idx_connection_loops_loop ON connection_loops(loop_id);
