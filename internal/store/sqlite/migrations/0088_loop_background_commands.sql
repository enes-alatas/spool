-- A loop's background commands (#1709). background_running is how many
-- commands the loop's live claude process last reported running, so a hub
-- that restarts knows the restart ended them. background_ended and
-- background_ended_by are the commands the hub ended and why (one of
-- store.BackgroundEnded*), owed to the loop at its next wake and cleared
-- once a turn has told it; 0 and '' while nothing is owed.
ALTER TABLE loops ADD COLUMN background_running INTEGER NOT NULL DEFAULT 0;
ALTER TABLE loops ADD COLUMN background_ended INTEGER NOT NULL DEFAULT 0;
ALTER TABLE loops ADD COLUMN background_ended_by TEXT NOT NULL DEFAULT '';
