-- Secrets are sealed at rest under the hub key (ADR-0046, #623). A sealed
-- hub MCP token can't be looked up by its value, since each seal draws a
-- fresh nonce, so a loop is found by the token's SHA-256 instead. The
-- column is filled, and its unique index made, when the store first opens
-- with the key: SQL has no SHA-256 to fill it with here. The old index on
-- the token goes, since a sealed value is never compared.
ALTER TABLE loops ADD COLUMN hub_mcp_token_hash TEXT NOT NULL DEFAULT '';
DROP INDEX idx_loops_hub_mcp_token;
