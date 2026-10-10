-- A hub user's link to the person they are on a surface (#703). A user
-- asks the control room for a link code and sends it to any loop's bot or
-- app in a DM; the sender is then linked to them. link_codes keeps a
-- code only by its SHA-256, one open code per user, until it is spent or
-- expires. user_links holds at most one person per user per surface, and
-- a person belongs to at most one user. Both go with their user.
CREATE TABLE link_codes (
    code_hash  TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);

CREATE TABLE user_links (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surface    TEXT NOT NULL,
    person     TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, surface)
);

-- From here on messages.dm_person (migration 0051) is also set on an
-- owner_dm message, to the owner it was with, so a later owner's user
-- does not read it. Older owner_dm rows keep '' and stay an admin's.
