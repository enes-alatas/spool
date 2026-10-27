-- The Claude login a contained loop runs on (ADR-0060 item 3): '' is the
-- default, which every existing loop keeps.
ALTER TABLE loops ADD COLUMN claude_login TEXT NOT NULL DEFAULT '';
