-- The operator's power-off switch. A health poll sees a stopped workstation
-- and a crashed one the same way, so the intent has to be recorded rather
-- than inferred — and recorded in the DB, so a workstation switched off
-- yesterday still reads "powered off" after an orchestrator restart instead
-- of "died overnight" (ADR-0021).
ALTER TABLE loops ADD COLUMN workstation_off INTEGER NOT NULL DEFAULT 0;
