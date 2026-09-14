-- Fleet rules: operator-defined rules every loop follows (ADR-0024). Enabled
-- rules render into every loop's system prompt ahead of its mission; disabled
-- ones stay stored as a decision, not an absence. Ids are creation-ordered and
-- the section renders in creation order, so it does not reshuffle between wakes.
CREATE TABLE fleet_rules (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
