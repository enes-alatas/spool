-- A cloud loop's GitHub repos and its claude.ai environment (ADR-0061
-- items 1 and 2): its sessions start from cloud_repo's cloud_branch in
-- cloud_environment_id and attach cloud_repos, a JSON list of owner/name.
-- '' for every other runtime's loop, and for a cloud loop made under
-- ADR-0059 until the operator sets them.
ALTER TABLE loops ADD COLUMN cloud_repo TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN cloud_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN cloud_repos TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN cloud_environment_id TEXT NOT NULL DEFAULT '';
