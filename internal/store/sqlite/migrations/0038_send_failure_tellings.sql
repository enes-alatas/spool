-- How many turns have carried a lost send's news to its loop (#561): the
-- first telling, then a reminder at each of the next two turns while the
-- failure is unresolved. At three the hub stops, and the failure is one the
-- loop left for the operator. A failure told before this column existed was
-- told under the once-only rule; it counts as done, so an upgrade does not
-- remind loops of failures that may be days old.
ALTER TABLE messages ADD COLUMN send_failure_tellings INTEGER NOT NULL DEFAULT 0;
UPDATE messages SET send_failure_tellings = 3 WHERE send_failure_told_at != 0;

-- A reminder skips a failure a later send claims to resend: the loop is told
-- of that send's own failure instead, and two lines for one set of words
-- would have it resolve the wrong link of the chain. The lookup is by the
-- claim, which most messages do not make.
CREATE INDEX idx_messages_resends ON messages(resends_id) WHERE resends_id != 0;
