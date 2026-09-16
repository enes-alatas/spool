-- The per-loop conversation queries — the control_room thread the web UI
-- polls and the owner_dm chat capture — filter on (conversation,
-- conversation_loop_id), which no index covers: a sparse private thread in
-- a table dominated by group chatter degrades toward a full scan. Trailing
-- id serves the newest-first ordering both queries use.
CREATE INDEX idx_messages_conversation ON messages(conversation, conversation_loop_id, id);
