-- A surface send that fails is no longer only a log line (#147). The failure
-- is recorded against the message it belongs to, so "did that reach them"
-- is answerable from the record rather than from a log grep: send_failed_at
-- is when the bridge gave up, send_error is what it gave up on, and both
-- clear if a later attempt succeeds.
ALTER TABLE messages ADD COLUMN send_failed_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN send_error TEXT NOT NULL DEFAULT '';
