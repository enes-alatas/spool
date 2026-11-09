-- Teams (ADR-0066): a named set of loops on one hub. A loop is in at most
-- one team, or in none, so its team is a column of the loop rather than a
-- membership table. NULL is no team: a column added with a reference may
-- default only to NULL. A rename follows into every loop; a team that still
-- has loops cannot be deleted.
CREATE TABLE teams (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
ALTER TABLE loops ADD COLUMN team TEXT REFERENCES teams(name) ON UPDATE CASCADE;
CREATE INDEX loops_team ON loops(team);

-- The upgrade (ADR-0066 item 4): the loops in the fleet channel already
-- work as one team, so they become the team named default. A loop the
-- operator took out of group stays in no team, and a hub with no loop in
-- group, a fresh install among them, gets no team at all.
INSERT INTO teams (name, created_at)
    SELECT 'default', CAST(strftime('%s', 'now') AS INTEGER) * 1000
    WHERE EXISTS (SELECT 1 FROM loops WHERE outside_fleet_channel = 0);
UPDATE loops SET team = 'default' WHERE outside_fleet_channel = 0;
