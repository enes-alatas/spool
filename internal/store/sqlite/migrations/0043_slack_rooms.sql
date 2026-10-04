-- Slack rooms (ADR-0038, #548): the channel a loop's Slack app was bound to
-- becomes the fleet channel's room on Slack, bound when it was, as 0034 did
-- for the Telegram group. Other Slack channels are recorded beside it.
INSERT INTO rooms (loop_id, surface, room_id, channel, first_seen_at, bound_at)
    SELECT id, 'slack', slack_channel_id, 'group', slack_channel_bound_at, slack_channel_bound_at
    FROM loops WHERE slack_channel_id <> '';
ALTER TABLE loops DROP COLUMN slack_channel_id;
ALTER TABLE loops DROP COLUMN slack_channel_bound_at;
