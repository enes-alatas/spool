-- A loop's owner is a chosen person, not whoever messaged its bot first
-- (#73). owner_tg_user_id names an allowlisted Telegram sender; the DM chat
-- between that person and *this loop's own bot* is captured when they
-- actually write to it, because a private chat id is the human's user id in
-- every bot's numbering and a bot can only write to a chat it has been
-- opened with. 0 in either column means "not configured" / "not ready", and
-- an owner_dm send says which.
ALTER TABLE loops ADD COLUMN owner_tg_user_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE loops ADD COLUMN owner_dm_chat_id INTEGER NOT NULL DEFAULT 0;

-- Existing loops inherit the first allowlisted sender as owner — the person
-- who has been talking to this fleet all along — and their DM chat when one
-- of their DMs to this loop's bot was recorded. A private chat's id is the
-- human's own user id, which is what makes that recovery possible.
-- COALESCE, because a database with loops and nobody allowlisted is
-- ordinary — a checkout that never connected Telegram — and a bare
-- subquery would write NULL into a NOT NULL column there, failing the
-- migration and stopping the server from opening its database at all.
-- Staying 0 is the supported "no owner configured" state.
UPDATE loops SET owner_tg_user_id = COALESCE((
    SELECT tg_user_id FROM tg_senders WHERE status = 'allowed'
    ORDER BY created_at, tg_user_id LIMIT 1
), 0) WHERE owner_tg_user_id = 0;

UPDATE loops SET owner_dm_chat_id = owner_tg_user_id
WHERE owner_tg_user_id != 0 AND EXISTS (
    SELECT 1 FROM messages m
    WHERE m.conversation = 'owner_dm' AND m.conversation_loop_id = loops.id
      AND m.tg_chat_id = loops.owner_tg_user_id
);
