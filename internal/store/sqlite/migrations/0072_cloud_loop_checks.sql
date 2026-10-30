-- A cloud loop's own setup steps on its setup view (ADR-0061 item 6,
-- #1182): the SHA-256 of the allowed-hosts text the operator confirmed
-- pasting into its environment, and when; and its last setup check, as
-- JSON (store.CloudCheck), '' for none. Every other loop has none.
ALTER TABLE loops ADD COLUMN cloud_allowlist_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN cloud_allowlist_confirmed_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE loops ADD COLUMN cloud_check TEXT NOT NULL DEFAULT '';
