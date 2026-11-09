-- A team's default Claude login (ADR-0066): a contained loop that names
-- none of its own runs on it. NULL is none, so the team's loops run on the
-- hub's default. A login a team names can't be deleted.
ALTER TABLE teams ADD COLUMN claude_login TEXT REFERENCES claude_logins(name);
