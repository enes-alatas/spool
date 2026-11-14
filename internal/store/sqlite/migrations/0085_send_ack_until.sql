-- How long the operator's acknowledgement of a send_failed alert silences
-- an unresolved failure (#1588): until this Unix ms, the hub's largest
-- integer for until it is resolved, 0 for never acknowledged. Kept on the
-- row, so a hub restart remembers it and a failure after the
-- acknowledgement is not covered by it.
ALTER TABLE messages ADD COLUMN send_ack_until INTEGER NOT NULL DEFAULT 0;
