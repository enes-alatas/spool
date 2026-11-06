-- A connection's last check (#1360): what the last check of the value it
-- holds found, as JSON (store.ConnectionCheck), '' for none. A new value or
-- a revoke clears it.
ALTER TABLE connections ADD COLUMN last_check TEXT NOT NULL DEFAULT '';
