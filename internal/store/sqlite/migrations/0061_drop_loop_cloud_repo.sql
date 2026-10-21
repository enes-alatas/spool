-- The repo field goes with the sessions client (ADR-0059): no session has
-- started from it since. A loop needing a repo gets a GH_TOKEN connection
-- and names the repo in its mission.
ALTER TABLE loops DROP COLUMN cloud_repo;
