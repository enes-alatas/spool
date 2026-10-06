-- A connection can be revoked (ADR-0043, #610): taken from every loop at
-- once and refused from then on. revoked_at is when, 0 for never. Its value
-- is retired as it is revoked, so the row keeps no live credential.
ALTER TABLE connections ADD COLUMN revoked_at INTEGER NOT NULL DEFAULT 0;
