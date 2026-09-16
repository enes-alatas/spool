-- Reply targets need a durable map from an internal message to how a given
-- bot sees it on Telegram (#79). A message_id means nothing on its own: it is
-- numbered per bot conversation (ADR-0020), so a bot can only render a native
-- reply against an id from its own numbering.
--
-- message_refs holds the exact ids we own: what a bot's poller received, and
-- what Telegram returned for a message that bot sent.
CREATE TABLE message_refs (
    message_id    INTEGER NOT NULL,
    bot_loop_id   TEXT    NOT NULL,
    tg_chat_id    INTEGER NOT NULL,
    tg_message_id INTEGER NOT NULL,
    PRIMARY KEY (message_id, bot_loop_id)
);

-- Already-ingested messages carry their receiving bot's id on the row.
INSERT INTO message_refs (message_id, bot_loop_id, tg_chat_id, tg_message_id)
SELECT id, tg_bot_loop_id, tg_chat_id, tg_message_id FROM messages
WHERE tg_bot_loop_id != '' AND tg_chat_id IS NOT NULL AND tg_message_id IS NOT NULL;

-- tg_sightings records every poller's own id for a group message, including
-- the bots that drop it for ingest. Without it only the elected ingest bot
-- could ever thread a reply under a human's group message. The sighting is
-- matched to the message by tg_key — chat, sender, Telegram's date and the
-- text — because that is all two bots observing the same message share.
-- A wrong match can only pick the wrong anchor between two identical
-- messages from the same sender in the same second; it never changes who a
-- message is delivered to.
CREATE TABLE tg_sightings (
    tg_key        TEXT    NOT NULL,
    bot_loop_id   TEXT    NOT NULL,
    tg_chat_id    INTEGER NOT NULL,
    tg_message_id INTEGER NOT NULL,
    seen_at       INTEGER NOT NULL,
    PRIMARY KEY (tg_key, bot_loop_id)
);
CREATE INDEX idx_tg_sightings_seen ON tg_sightings(seen_at);

-- tg_key is set on ingest so a message can find the other bots' sightings of
-- it; reply_to_id records the message a reply was aimed at.
ALTER TABLE messages ADD COLUMN tg_key TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN reply_to_id INTEGER NOT NULL DEFAULT 0;
