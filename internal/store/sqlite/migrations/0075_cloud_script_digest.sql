-- The digest of the setup script a cloud loop's environment last reported
-- running, from a SessionStart post the hub took; '' before any (#1381).
ALTER TABLE loops ADD COLUMN cloud_script_digest TEXT NOT NULL DEFAULT '';
