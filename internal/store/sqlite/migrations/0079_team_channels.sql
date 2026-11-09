-- A team's channels (ADR-0066): a channel belongs to at most one team, and
-- NULL is a hub channel, as every channel was before. A team's rename
-- follows into its channels; a team is deleted only with its channels.
ALTER TABLE channels ADD COLUMN team TEXT REFERENCES teams(name) ON UPDATE CASCADE;
CREATE INDEX channels_team ON channels(team);

-- A team's home channel, the one made with it, which every loop put in the
-- team joins. NULL is none: the upgrade's default team has none, and
-- deleting the channel leaves the team without one.
ALTER TABLE teams ADD COLUMN home_channel TEXT REFERENCES channels(name) ON DELETE SET NULL;
