-- A Claude login's plan usage (ADR-0060 item 4), a store.PlanUsageRecord
-- as JSON, '' before its first observation. The default login's stays in
-- settings.plan_usage, which every hub has observed into until now.
ALTER TABLE claude_logins ADD COLUMN plan_usage TEXT NOT NULL DEFAULT '';
