-- Connections (ADR-0043): org-level tool credentials and configs, defined
-- once under a name for loops to be attached to. config is the kind's JSON
-- and is read back in full; secret is write-only through the API and never
-- logged, the same rule loop_secrets (0004) follows. An mcp-server may have
-- no secret, so it defaults empty.
CREATE TABLE connections (
    name       TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    config     TEXT NOT NULL,
    secret     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
