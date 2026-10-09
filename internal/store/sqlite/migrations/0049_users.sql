-- The hub's users and their sign-in sessions (ADR-0048, #675). A password
-- is kept only as a self-describing PBKDF2 hash. A session is kept by its
-- ID's SHA-256, so a copy of the database holds no live session; user_id
-- is NULL for a session the operator token opened, which belongs to no
-- user, and token_hash is then that token's SHA-256, so the session ends
-- when the token changes. A removed user's sessions go with them.
CREATE TABLE users (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL UNIQUE,
    role                 TEXT NOT NULL,
    password_hash        TEXT NOT NULL,
    must_change_password INTEGER NOT NULL DEFAULT 0,
    created_at           INTEGER NOT NULL
);

CREATE TABLE user_sessions (
    id_hash      TEXT PRIMARY KEY,
    user_id      TEXT REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
);
CREATE INDEX idx_user_sessions_user ON user_sessions(user_id);

-- The per-username sign-in throttle, kept by the name tried rather than by
-- user, so an unknown name locks as a real one does and a lock does not
-- say which names exist.
CREATE TABLE sign_in_throttle (
    name            TEXT PRIMARY KEY,
    failures        INTEGER NOT NULL,
    locked_until    INTEGER NOT NULL DEFAULT 0,
    last_failure_at INTEGER NOT NULL
);
