-- The GitHub repo a cloud loop's sessions start from, '' for none and for
-- every other runtime's loop (ADR-0056). Set at creation only.
ALTER TABLE loops ADD COLUMN cloud_repo TEXT NOT NULL DEFAULT '';
