-- A loop works alone when subagents is 'off': every turn's claude is
-- denied the Agent tool (#855). Every existing loop keeps them.
ALTER TABLE loops ADD COLUMN subagents TEXT NOT NULL DEFAULT 'allowed' CHECK (subagents IN ('allowed','off'));
