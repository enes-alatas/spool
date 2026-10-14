-- A person's edit or deletion of a message they said on a surface
-- (ADR-0055, #810). The correction is a message of its own: edit_of is the
-- message it corrects and edit_kind whether it edits or deletes it.
-- edit_key names the edit as every loop's bot or app that heard it sees it
-- alike, so a shared room's edit is stored once. The original records when
-- it was edited or deleted, and keeps its text through an edit but not a
-- deletion.
ALTER TABLE messages ADD COLUMN edit_of INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN edit_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN edit_key TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN edited_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX idx_messages_edit_key ON messages(edit_key) WHERE edit_key != '';
