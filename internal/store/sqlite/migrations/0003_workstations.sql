-- Per-loop workstation config (ADR-0017, ADR-0018). Existing loops predate
-- the docker runtime and were all created bare, so the backfill is truthful:
-- bare loops have no workstation, hence no image and no limits.
ALTER TABLE loops ADD COLUMN runtime TEXT NOT NULL DEFAULT 'bare' CHECK (runtime IN ('bare','docker'));
ALTER TABLE loops ADD COLUMN image TEXT NOT NULL DEFAULT '';
ALTER TABLE loops ADD COLUMN mem_mb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE loops ADD COLUMN cpus REAL NOT NULL DEFAULT 0;
