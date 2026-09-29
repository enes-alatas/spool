-- A Slack message's identity (#230): the channel it was posted in and its
-- ts, which Slack numbers per channel rather than per app, so every loop's
-- app in a channel sees the same pair for the same message. That makes the
-- pair a key, and the key is the ingest election on Slack (ADR-0020): of
-- the links that hear one message, the first to insert it stores it and
-- the others find it already there. '' for a message that did not come
-- from Slack, which the index leaves out.
ALTER TABLE messages ADD COLUMN slack_channel_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN slack_ts TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_messages_slack ON messages(slack_channel_id, slack_ts) WHERE slack_ts != '';
