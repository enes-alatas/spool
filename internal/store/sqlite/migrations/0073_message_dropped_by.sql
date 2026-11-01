-- The loops that dropped a message after its turn crashed every process
-- that ran it (#1205, #1189), as a JSON array of loop IDs like
-- delivered_to: a group message reaches several loops, and one dropping
-- it says nothing of the others.
ALTER TABLE messages ADD COLUMN dropped_by TEXT NOT NULL DEFAULT '[]';
