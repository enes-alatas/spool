-- A connection attached to a team as a whole (ADR-0066, #1610): every loop
-- in the team holds it, those that join later included. A team's rename
-- carries its attachments and its deletion takes them; a connection
-- attached to a team is not deleted.
CREATE TABLE team_connections (
    team        TEXT NOT NULL REFERENCES teams(name) ON UPDATE CASCADE ON DELETE CASCADE,
    connection  TEXT NOT NULL REFERENCES connections(name),
    attached_at INTEGER NOT NULL,
    PRIMARY KEY (team, connection)
);
CREATE INDEX idx_team_connections_connection ON team_connections(connection);
