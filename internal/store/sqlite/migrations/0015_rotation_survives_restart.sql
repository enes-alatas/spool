-- A rotation is a decision the loop has already acted on, so it must outlive
-- the orchestrator (#66). Both columns are actor state that was memory-only:
--
-- rotate_pending is set the moment the handoff turn is asked for — from then
-- on the session has been told "this session ends here", and a restart that
-- resumed it would resurrect a session its own last turn retired.
--
-- handoff_note holds the note the retired session wrote for its successor
-- until a turn completes on that successor. Losing it across a restart cost
-- the loop a paid turn's worth of continuity and opened the fresh session
-- with the "could not be resumed" preamble, which a deliberate rotation is
-- not (ADR-0022).
ALTER TABLE loops ADD COLUMN rotate_pending INTEGER NOT NULL DEFAULT 0;
ALTER TABLE loops ADD COLUMN handoff_note TEXT NOT NULL DEFAULT '';
