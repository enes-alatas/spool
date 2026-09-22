-- A failed send stayed failed. Nothing recorded that the loop said it again
-- and the second attempt landed, or that the operator read the failure and
-- moved on — so the undelivered count only fell when the 24-hour window slid
-- past it, which is a clock deciding when the operator is done looking (#269).
--
-- send_resolved_at is when the failure stopped being news, and it replaces
-- the window: the count and the Undelivered tab now ask for failures that are
-- unresolved, at any age. A row keeps its send_failed_at and send_error when
-- it resolves — what failed and why is history worth keeping (#201), and
-- clearing it would make a retried message look like one that never failed.
ALTER TABLE messages ADD COLUMN send_resolved_at INTEGER NOT NULL DEFAULT 0;

-- How it resolved, because the two ways are not the same fact to a loop. A
-- retry that landed means the message did arrive, so the sender must not be
-- told it was lost — told that, a loop says it again and the human gets it
-- twice, which is the doubling ADR-0026's amendment exists to prevent. A
-- dismissal means the operator read the failure and moved on; the message
-- still never arrived, and its sender is still owed that news.
ALTER TABLE messages ADD COLUMN send_resolution TEXT NOT NULL DEFAULT '';

-- The list's index follows the predicate it serves. 0021's index was partial
-- on send_failed_at!=0 alone; resolved rows now sit outside the question
-- entirely, so they belong outside the index rather than in it being filtered
-- out one by one.
DROP INDEX IF EXISTS idx_messages_failed_at;
CREATE INDEX idx_messages_unresolved ON messages(send_failed_at DESC, id DESC)
  WHERE send_failed_at != 0 AND send_resolved_at = 0;

-- And the per-loop count's, for the same reason: idx_messages_send_failed
-- stays for UntoldSendFailures, which asks a different question (has the
-- loop been told), but the badge's count is this one.
CREATE INDEX idx_messages_unresolved_by_loop ON messages(from_loop_id, send_failed_at)
  WHERE send_failed_at != 0 AND send_resolved_at = 0;
