-- A hub holds named Claude logins (ADR-0060, #1069). account is the
-- login's sign-in, a store.CloudAccount as JSON sealed under the hub key
-- (ADR-0046), '' while it is signed out: a signed-out login still exists,
-- so the loops on it keep a valid name. owner_user_id is the user who
-- added it, '' for the one the upgrade made. check_record is its last
-- login check (ADR-0044), a store.LoginCheckRecord as JSON, '' before
-- the first; the default login's stays in settings.login_check, which
-- onboarding reads.
CREATE TABLE claude_logins (
  name          TEXT PRIMARY KEY,
  account       TEXT NOT NULL DEFAULT '',
  owner_user_id TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  check_record  TEXT NOT NULL DEFAULT ''
);

-- Today's sign-in becomes the login named default. Its value is sealed
-- already, and a sealed value carries nothing of where it is kept, so it
-- moves as it is.
INSERT INTO claude_logins (name, account, created_at)
  SELECT 'default', value, CAST(strftime('%s','now') AS INTEGER) * 1000
  FROM settings WHERE key = 'cloud_account' AND value <> '';
DELETE FROM settings WHERE key = 'cloud_account';
