-- The hub's one order of its teams (ADR-0066, #1451): a team's place in it.
-- No team's place is the setting team_order_no_team, absent while nobody
-- has set an order, which puts No team last. Teams already made keep the
-- order the Fleet page showed them in, by name.
ALTER TABLE teams ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
UPDATE teams SET position = (SELECT COUNT(*) FROM teams AS earlier WHERE earlier.name < teams.name);
