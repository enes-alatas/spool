-- The hub's health check (ADR-0053, #764): a problem it found with a loop,
-- open from since until it clears, by the operator acting on the loop or by
-- the signal itself clearing. A loop has at most one open problem per
-- symptom; a cleared one stays as the record of what happened, and the same
-- symptom again opens a new row.
CREATE TABLE loop_problems (
    id         INTEGER PRIMARY KEY,
    loop_id    TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    symptom    TEXT NOT NULL,
    since      INTEGER NOT NULL,
    cleared_at INTEGER NOT NULL DEFAULT 0,
    cleared_by TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX loop_problems_open ON loop_problems (loop_id, symptom) WHERE cleared_at = 0;
