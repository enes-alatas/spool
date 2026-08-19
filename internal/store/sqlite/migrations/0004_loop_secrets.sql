-- Per-loop secret env vars: tool credentials (gh token, API keys) injected into
-- every workstation exec until an org-level connections catalog replaces them.
-- Values are write-only through the API and never logged. ON DELETE CASCADE ties
-- a secret's life to its loop, like sessions and turns.
CREATE TABLE loop_secrets (
    loop_id    TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (loop_id, name)
);
