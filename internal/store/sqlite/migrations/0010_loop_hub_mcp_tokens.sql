-- Each loop authenticates to the hub's MCP endpoint with its own bearer
-- token (ADR-0026). New loops get a token minted at creation; existing
-- loops are backfilled here so the fleet needs no manual step. The unique
-- index is the auth lookup path and guards against a collision ever
-- silently aliasing two loops.
ALTER TABLE loops ADD COLUMN hub_mcp_token TEXT NOT NULL DEFAULT '';
UPDATE loops SET hub_mcp_token = lower(hex(randomblob(24)));
CREATE UNIQUE INDEX idx_loops_hub_mcp_token ON loops(hub_mcp_token);
