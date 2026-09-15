-- Every message belongs to exactly one conversation — owner_dm, group, or
-- control_room — the unit of privacy and addressing (ADR-0026). The private
-- kinds are keyed to their loop in conversation_loop_id; group rows leave it
-- empty.
--
-- Existing rows are classified best-effort from what was recorded:
--   telegram-dm    → that loop's owner_dm (the receiving bot is the loop; rows
--                    from before migration 0005 lack tg_bot_loop_id and fall
--                    back to the delivered target)
--   telegram-group → group
--   loop           → group (loop replies were posted to the bound group)
--   web            → the loop's control_room when it addressed exactly one
--                    loop (the per-loop composer); the retired broadcast
--                    composer's rows go to group, where the old unconditional
--                    mirror showed them
ALTER TABLE messages ADD COLUMN conversation TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN conversation_loop_id TEXT NOT NULL DEFAULT '';

UPDATE messages SET conversation = 'group'
WHERE origin IN ('telegram-group', 'loop');

UPDATE messages SET
    conversation = 'owner_dm',
    conversation_loop_id = CASE
        WHEN tg_bot_loop_id != '' THEN tg_bot_loop_id
        ELSE COALESCE(json_extract(delivered_to, '$[0]'), '')
    END
WHERE origin = 'telegram-dm';

UPDATE messages SET
    conversation = CASE
        WHEN json_array_length(delivered_to) = 1 THEN 'control_room'
        ELSE 'group'
    END,
    conversation_loop_id = CASE
        WHEN json_array_length(delivered_to) = 1 THEN json_extract(delivered_to, '$[0]')
        ELSE ''
    END
WHERE origin = 'web';
