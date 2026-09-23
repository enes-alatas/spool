-- A loop may be outside the fleet channel (ADR-0032 item 2): no group, so
-- @all does not reach it, a mention of it delivers to nobody, and its own
-- send to the group is refused. Stored as the exception rather than as
-- membership, so every existing loop — which has been in the one group all
-- along — stays in it with no backfill, and so does every new one unless
-- the operator takes it out.
ALTER TABLE loops ADD COLUMN outside_fleet_channel INTEGER NOT NULL DEFAULT 0;
