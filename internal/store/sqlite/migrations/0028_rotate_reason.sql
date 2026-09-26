-- Why the rotation in progress was taken (#272): 'fill', 'operator' or
-- 'mission', '' while none is. It travels with handoff_note, because the
-- fresh session is told why its predecessor stopped, and a restart between
-- the handoff turn and that session must not lose the reason.
ALTER TABLE loops ADD COLUMN rotate_reason TEXT NOT NULL DEFAULT '';
