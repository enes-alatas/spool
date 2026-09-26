-- A loop's identity on Slack (#230): one app per loop, so the app-level
-- token (Socket Mode) and the bot token travel together, with what the bot
-- token's own auth.test answered. The channel is the room the fleet channel
-- is mirrored to, bound the way a Telegram group is (ADR-0020, ADR-0032).
-- The owner is an allowlisted Slack sender, and the DM channel with them is
-- the one the bot opened, kept so every owner_dm send does not re-open it.
-- '' / 0 everywhere means "not configured", as for the tg_ columns.
ALTER TABLE loops ADD COLUMN slack_app_token TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN slack_bot_token TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN slack_bot_user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN slack_bot_name TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN slack_team_id TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN slack_team_name TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN slack_channel_id TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN slack_channel_bound_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE loops ADD COLUMN owner_slack_user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN owner_slack_dm_channel TEXT NOT NULL DEFAULT '';

-- Spool's pairing allowlist for Slack, the same flow as tg_senders:
-- workspace membership alone admits nobody (operator, 2026-09-22). Keyed by
-- the user id alone because a hub's Slack loops share one workspace (#230);
-- team_id is kept so a row says which workspace it came from.
CREATE TABLE slack_senders (
    slack_user_id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    display TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','allowed','blocked')),
    pair_code TEXT NOT NULL DEFAULT '',
    first_seen_via TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
