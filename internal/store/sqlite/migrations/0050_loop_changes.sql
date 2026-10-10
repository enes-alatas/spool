-- Who changed a loop (ADR-0048, #695). loop_changes is append-only, one
-- row per change a user or the operator token made through the API. Like
-- connection_events it names the loop rather than referencing it, and
-- keeps the loop's name as it was, so a row outlives a deleted loop.
-- actor is the user's name, or 'operator' for the operator token.
--
-- created_by and updated_by are the same names, kept on the loop for its
-- page: who made it, and who changed it last. A loop made before this
-- release has neither, since nothing recorded them.
CREATE TABLE loop_changes (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    at        INTEGER NOT NULL,
    actor     TEXT NOT NULL,
    action    TEXT NOT NULL,
    loop_id   TEXT NOT NULL,
    loop_name TEXT NOT NULL
);
CREATE INDEX idx_loop_changes_loop ON loop_changes(loop_id);
CREATE INDEX idx_loop_changes_loop_name ON loop_changes(loop_name);

ALTER TABLE loops ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN updated_by TEXT NOT NULL DEFAULT '';
