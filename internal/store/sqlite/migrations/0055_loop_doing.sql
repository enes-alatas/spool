-- A loop's doing: the one line it sets with a [doing: …] trailer to say what
-- it is working on, and when it set it (#751). Kept until the loop replaces
-- or clears it.
ALTER TABLE loops ADD COLUMN doing TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN doing_at INTEGER NOT NULL DEFAULT 0;
