-- Private conversations with any allowed sender (ADR-0049, #702).
--
-- private_chats records that a person who is not the loop's owner has
-- messaged this loop's bot or app privately, and where: a loop writes
-- first only to someone who has (#100). The owner's chat stays on the loop
-- row, where #73 put it. person is the sender's stable key on its surface,
-- 'tg:<user id>' or 'slack:<user id>'; chat is the Telegram chat id, in
-- decimal, or the Slack DM channel.
CREATE TABLE private_chats (
    loop_id     TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    person      TEXT NOT NULL,
    chat        TEXT NOT NULL,
    captured_at INTEGER NOT NULL,
    PRIMARY KEY (loop_id, person)
);

-- dm_person is the same key on a message of one of those conversations,
-- whose conversation is 'dm:<name>'. The name is how the person read when
-- the message was written; the key is who they are, so a renamed person is
-- still the same conversation.
ALTER TABLE messages ADD COLUMN dm_person TEXT NOT NULL DEFAULT '';
