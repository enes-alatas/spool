-- A cloud loop's hub token is the environment's network secret from here
-- on, shown once and kept only as its hash (ADR-0061). A loop made under
-- ADR-0059 holds a per-session token its sessions carried in their VMs,
-- which nothing can use now: it is revoked, and the loop is held until
-- Rotate token mints another.
UPDATE loops SET hub_mcp_token = '', hub_mcp_token_hash = '' WHERE runtime = 'cloud';
