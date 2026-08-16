ALTER TABLE loops ADD COLUMN pacing TEXT NOT NULL DEFAULT 'fixed' CHECK (pacing IN ('fixed','self'));
ALTER TABLE loops ADD COLUMN effort TEXT NOT NULL DEFAULT '';

CREATE TABLE tg_senders (
    tg_user_id INTEGER PRIMARY KEY,
    username TEXT NOT NULL DEFAULT '',
    display TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','allowed','blocked')),
    pair_code TEXT NOT NULL DEFAULT '',
    first_seen_via TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
