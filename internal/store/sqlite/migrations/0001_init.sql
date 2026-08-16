CREATE TABLE loops (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    mission TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    workspace_mode TEXT NOT NULL DEFAULT 'none' CHECK (workspace_mode IN ('none','dir','worktree')),
    workspace_path TEXT NOT NULL DEFAULT '',
    repo_path TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    tick_interval_sec INTEGER NOT NULL DEFAULT 1800,
    min_wake_sec INTEGER NOT NULL DEFAULT 300,
    max_wake_sec INTEGER NOT NULL DEFAULT 14400,
    idle_timeout_sec INTEGER NOT NULL DEFAULT 90,
    tg_bot_token TEXT NOT NULL DEFAULT '',
    tg_bot_username TEXT NOT NULL DEFAULT '',
    tg_group_chat_id INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','archived')),
    current_session_id TEXT NOT NULL DEFAULT '',
    current_pid INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    loop_id TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    started_at INTEGER NOT NULL,
    ended_at INTEGER NOT NULL DEFAULT 0,
    end_reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_sessions_loop ON sessions(loop_id, started_at);

CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts INTEGER NOT NULL,
    origin TEXT NOT NULL,
    author TEXT NOT NULL DEFAULT '',
    from_loop_id TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    mentions TEXT NOT NULL DEFAULT '[]',
    tg_chat_id INTEGER,
    tg_message_id INTEGER,
    delivered_to TEXT NOT NULL DEFAULT '[]',
    UNIQUE (tg_chat_id, tg_message_id)
);
CREATE INDEX idx_messages_ts ON messages(ts);

CREATE TABLE turns (
    id TEXT PRIMARY KEY,
    loop_id TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    trigger_kind TEXT NOT NULL DEFAULT 'manual',
    started_at INTEGER NOT NULL,
    ended_at INTEGER NOT NULL DEFAULT 0,
    is_error INTEGER NOT NULL DEFAULT 0,
    result_text TEXT NOT NULL DEFAULT '',
    cost_usd REAL NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_turns_loop ON turns(loop_id, started_at);

CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    loop_id TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL DEFAULT '',
    ts INTEGER NOT NULL,
    type TEXT NOT NULL,
    subtype TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_events_loop ON events(loop_id, id);

CREATE TABLE schedule (
    loop_id TEXT PRIMARY KEY REFERENCES loops(id) ON DELETE CASCADE,
    next_tick_at INTEGER NOT NULL DEFAULT 0,
    last_tick_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE inbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    loop_id TEXT NOT NULL REFERENCES loops(id) ON DELETE CASCADE,
    envelope TEXT NOT NULL,
    queued_at INTEGER NOT NULL
);
CREATE INDEX idx_inbox_loop ON inbox(loop_id, id);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
