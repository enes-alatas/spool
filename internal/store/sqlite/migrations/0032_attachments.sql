-- Files that cross a chat surface with a message (#123). One row per file:
-- a Slack message can carry several, a Telegram one a photo or a document.
-- The file itself is kept once under the hub's files directory; path is
-- relative to it. Retention removes the file after 30 days and sets
-- removed_at, and the row stays, so a message still says what it carried.
-- A file that never arrived has no path and says why in not_kept.
CREATE TABLE attachments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL,
    name       TEXT    NOT NULL,
    mime       TEXT    NOT NULL DEFAULT '',
    kind       TEXT    NOT NULL,
    size       INTEGER NOT NULL,
    sha256     TEXT    NOT NULL DEFAULT '',
    width      INTEGER NOT NULL DEFAULT 0,
    height     INTEGER NOT NULL DEFAULT 0,
    path       TEXT    NOT NULL DEFAULT '',
    not_kept   TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    removed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_attachments_message ON attachments(message_id, id);
CREATE INDEX idx_attachments_kept ON attachments(created_at) WHERE removed_at = 0 AND path != '';
