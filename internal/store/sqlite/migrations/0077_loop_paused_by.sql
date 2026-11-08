-- Who paused a loop, kept until it is resumed (ADR-0067, #1404): a user's
-- name, "operator" for the operator token, or a management token's actor.
-- A management token resumes only a loop one paused, so a loop paused
-- before this column, or paused by anything that names no one, reads as
-- a person's pause and stays theirs to resume.
ALTER TABLE loops ADD COLUMN paused_by TEXT NOT NULL DEFAULT '';
