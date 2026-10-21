-- A cloud turn reports no cost, only its tokens, so the hub prices them
-- as an estimate, and a model it has no price for leaves the cost unknown
-- (ADR-0059). cost_usd stays per turn and summable: an unknown cost is
-- stored as 0 with cost_unknown set, so a sum leaves it out and the API
-- serves it as null, never as free.
ALTER TABLE turns ADD COLUMN cost_estimated INTEGER NOT NULL DEFAULT 0;
ALTER TABLE turns ADD COLUMN cost_unknown INTEGER NOT NULL DEFAULT 0;
