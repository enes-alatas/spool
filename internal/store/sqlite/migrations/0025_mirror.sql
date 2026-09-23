-- Whether a message exists on the surface too (ADR-0032 item 6, #285): one
-- of not_mirrored, pending, mirrored. It answers what send_failed_at and
-- send_error cannot, since those only ever describe an attempt that
-- happened: an operator post and a loop with no surface both produce no
-- attempt, and "no attempt" is not "no failure". Spelled, never '' once a
-- row is written, so a client reading an absent field knows it is talking
-- to an older server rather than being told something about the message.
ALTER TABLE messages ADD COLUMN mirror TEXT NOT NULL DEFAULT '';

-- Existing rows, from what the store can prove.
--
-- Everything that came in from Telegram is on Telegram. A loop's own send to
-- the group or its owner's DM went out unless it failed: before this column,
-- no loop could exist without a bot, so every such send with no failure
-- standing against it was mirrored. The one case that reads wrong is a send
-- made while its loop's bot was not yet bound to the group, which went
-- nowhere and cannot be told apart now; it is rare, and saying "on the hub
-- only" of the rest would mislabel real Telegram history instead.
--
-- A failure is pending until it resolves: a delivered retry mirrored it, a
-- dismissal or a resend left this row on the hub for good. Any other row
-- that was sent out — the operator's "(via web)" group posts before #293 —
-- has the reference Telegram returned for it, and is mirrored too.
UPDATE messages SET mirror = CASE
    WHEN origin IN ('telegram-group', 'telegram-dm') THEN 'mirrored'
    WHEN send_failed_at != 0 AND send_resolved_at = 0 THEN 'pending'
    WHEN send_failed_at != 0 AND send_resolution = 'delivered' THEN 'mirrored'
    WHEN send_failed_at != 0 THEN 'not_mirrored'
    WHEN origin = 'loop' AND conversation IN ('group', 'owner_dm') THEN 'mirrored'
    WHEN EXISTS (SELECT 1 FROM message_refs r WHERE r.message_id = messages.id) THEN 'mirrored'
    ELSE 'not_mirrored'
END;

-- The startup sweep asks for sends left in flight by a stopped hub.
CREATE INDEX idx_messages_mirror_pending ON messages(id) WHERE mirror = 'pending' AND send_failed_at = 0;
