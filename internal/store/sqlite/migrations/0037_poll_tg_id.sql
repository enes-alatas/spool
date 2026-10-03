-- The id Telegram gave a poll a loop's bot sent (ADR-0041). Telegram reports
-- each vote by this id alone, with no chat or message, so it is how a vote
-- finds its poll. '' until the poll is sent, and for a poll no bot sent.
ALTER TABLE polls ADD COLUMN tg_poll_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX polls_tg_poll_id ON polls (tg_poll_id) WHERE tg_poll_id <> '';
