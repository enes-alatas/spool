-- The tokens an operator's agent holds to manage the hub (ADR-0067, #1402).
-- A token is kept only as its SHA-256, so a copy of the database holds no
-- usable one, and revoking it deletes its row. owner_user_id is NULL for a
-- token the operator token minted, which belongs to no user; a removed
-- user's tokens go with them. expires_at is 0 for a token with no end.
CREATE TABLE management_tokens (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    owner_user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    token_hash    TEXT NOT NULL UNIQUE,
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL DEFAULT 0,
    last_used_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_management_tokens_owner ON management_tokens(owner_user_id);
