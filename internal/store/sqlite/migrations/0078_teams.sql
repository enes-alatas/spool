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
