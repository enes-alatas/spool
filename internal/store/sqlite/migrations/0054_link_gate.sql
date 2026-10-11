-- A link code is the way a person becomes an allowed sender (#730,
-- ADR-0051). A code is minted for one surface and links only there; codes
-- open at the upgrade were minted for none, so they go, as any code does
-- within 15 minutes.
DELETE FROM link_codes;
ALTER TABLE link_codes ADD COLUMN surface TEXT NOT NULL DEFAULT '';

-- A spent code links nobody until its sender replies YES. Until then the
-- sender holds one confirmation, keyed by who they are on the surface,
-- with how the surface named them and the loop whose bot or app the code
-- reached, for the control room to show.
CREATE TABLE link_confirmations (
    person     TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    loop_id    TEXT NOT NULL DEFAULT '',
    username   TEXT NOT NULL DEFAULT '',
    display    TEXT NOT NULL DEFAULT '',
    team_id    TEXT NOT NULL DEFAULT '',
    expires_at INTEGER NOT NULL
);

-- An append-only record of links made and removed, for admins. It names
-- the user and the loop as they were, so a row outlives both.
CREATE TABLE link_changes (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    at        INTEGER NOT NULL,
    action    TEXT NOT NULL CHECK (action IN ('link','unlink')),
    user_name TEXT NOT NULL,
    surface   TEXT NOT NULL,
    person    TEXT NOT NULL,
    loop_name TEXT NOT NULL DEFAULT ''
);
