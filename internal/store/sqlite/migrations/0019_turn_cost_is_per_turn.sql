-- cost_usd held the CLI's total_cost_usd, which under --resume is the
-- session's running total rather than the price of the turn that just ran.
-- Every sum over turns therefore overstated spend by up to an order of
-- magnitude, and the control room's cost figures are built on such a sum
-- (#191).
--
-- The cumulative figure keeps a column of its own, named so nobody sums it
-- again, and cost_usd becomes what its name always promised.
ALTER TABLE turns ADD COLUMN session_cost_usd REAL NOT NULL DEFAULT 0;

UPDATE turns SET session_cost_usd = cost_usd;

-- SessionCost runs on every turn finish, and idx_turns_loop is keyed
-- (loop_id, started_at), so without this it scans every turn the loop ever
-- ran. Covering, so the MAX is answered from the index alone.
--
-- Built before the backfill below, which runs that same per-session MAX once
-- per row: unindexed it rescans the table each time, quadratic in a store's
-- history. Migrations block startup, so that reads as a hung orchestrator.
CREATE INDEX idx_turns_session_cost ON turns(loop_id, session_id, session_cost_usd);

-- Each turn's cost is what the session's total gained; the first turn of a
-- session gained all of it. Clamped at zero: a total that fails to grow
-- means the turn spent nothing, never that it earned money back.
--
-- Priced against the highest total the session had reached, not against the
-- immediately preceding row, so that this agrees with the running engine by
-- construction (SessionCost is the same MAX). The two differ precisely when
-- a total dips: a turn reporting 0 after 8.92 would otherwise leave the next
-- turn priced against 0 and charged for the whole session — the bug this
-- migration exists to repair, written back into the history it repairs.
UPDATE turns SET cost_usd = MAX(0, session_cost_usd - COALESCE((
    SELECT MAX(prev.session_cost_usd) FROM turns AS prev
    WHERE prev.loop_id = turns.loop_id
      AND prev.session_id = turns.session_id
      AND (prev.started_at < turns.started_at
           OR (prev.started_at = turns.started_at AND prev.id < turns.id))
), 0));
