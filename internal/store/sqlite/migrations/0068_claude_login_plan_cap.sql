-- When a Claude login's Resume now stops holding its plan cap off
-- (ADR-0060 item 5), in Unix ms, 0 when there has been none. The default
-- login's stays in settings.plan_cap_resumed_until.
ALTER TABLE claude_logins ADD COLUMN plan_cap_resumed_until INTEGER NOT NULL DEFAULT 0;
