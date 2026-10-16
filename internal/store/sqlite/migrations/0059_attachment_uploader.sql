-- Who uploaded a file from the control room: their user id, or '' for the
-- operator token, a surface's file and a loop's. Until a message claims an
-- upload, only its uploader or an admin may read or send it.
ALTER TABLE attachments ADD COLUMN uploaded_by TEXT NOT NULL DEFAULT '';
