-- Visibility (ADR-0023): every message is coordination (loop-addressed) or
-- human-facing (addressed to a human, or a reply to a human-triggered turn).
-- Existing rows predate the distinction and were all mirrored to surfaces
-- under the old unconditional-mirror behavior, so they backfill as
-- human-facing — the reading closest to what actually happened.
ALTER TABLE messages ADD COLUMN visibility TEXT NOT NULL DEFAULT 'human-facing';
