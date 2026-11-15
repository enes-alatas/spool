-- The record names the team of a team attach or detach (#1610), '' for
-- every other change.
ALTER TABLE connection_events ADD COLUMN team TEXT NOT NULL DEFAULT '';
