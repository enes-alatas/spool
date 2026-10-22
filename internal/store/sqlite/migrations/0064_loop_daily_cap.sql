-- A loop's own daily USD cap (#652): at or above it, the loop sleeps until
-- the hub's local day resets. 0 is no cap, which every existing loop keeps.
ALTER TABLE loops ADD COLUMN daily_cap_usd REAL NOT NULL DEFAULT 0 CHECK (daily_cap_usd >= 0);
