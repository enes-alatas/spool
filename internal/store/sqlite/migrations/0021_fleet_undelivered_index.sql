-- The Undelivered tab reads every loop's failed sends at once (#263), which
-- is the one scope idx_messages_send_failed cannot serve: its leading column
-- is from_loop_id, and a fleet-wide query has no sender to name. A `!=` on a
-- leading column is not seekable, so that query scans the whole of a table
-- messages are never pruned from (docs/QUALITY.md) and then sorts the
-- result in a temp B-tree.
--
-- Partial, on the predicate both callers share: one entry per failure rather
-- than one per message, in a table where a failure is rare by construction.
-- The DESC pair matches the list's order exactly, so the ORDER BY comes off
-- the index instead of a sort.
CREATE INDEX idx_messages_failed_at ON messages(send_failed_at DESC, id DESC)
  WHERE send_failed_at != 0;
