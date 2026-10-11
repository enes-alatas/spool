-- A loop's private conversations are read per person (#720): one person's
-- dm:<name> rows under any name, and the people a loop holds a private
-- conversation with. Both filter on (conversation_loop_id, dm_person),
-- which idx_messages_conversation cannot serve, since a dm's conversation
-- carries the person's name of the day. Trailing id serves newest first.
CREATE INDEX idx_messages_dm_person ON messages(conversation_loop_id, dm_person, id);
