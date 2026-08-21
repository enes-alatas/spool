-- The model a turn actually ran on, as the CLI reported it at init. A loop's
-- configured model may be empty ("whatever the CLI resolves to") or an alias
-- that floats between releases, so it cannot answer "how big is this loop's
-- context window" — the turn can.
ALTER TABLE turns ADD COLUMN model TEXT NOT NULL DEFAULT '';
