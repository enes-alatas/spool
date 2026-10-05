-- A connection's value can be rotated (ADR-0043, #609). rotated_at is when
-- it last was, 0 for never. A value replaced or deleted stays a retired
-- secret: the redactor keeps masking it, because a credential the hub has
-- let go of may still open something upstream, and it is never read back
-- out or injected (Enes, 2026-10-05, #507). redact_name is the name the
-- redactor showed it under while it was live, and no foreign key ties a
-- row to its connection, so deleting the connection keeps the row.
ALTER TABLE connections ADD COLUMN rotated_at INTEGER NOT NULL DEFAULT 0;
CREATE TABLE retired_secrets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    connection  TEXT NOT NULL,
    redact_name TEXT NOT NULL,
    value       TEXT NOT NULL,
    retired_at  INTEGER NOT NULL
);
