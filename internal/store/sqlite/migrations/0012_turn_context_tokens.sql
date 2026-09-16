-- Context occupancy of a turn's last API call: what the session's next
-- prompt would carry into the model's window. The existing token columns are
-- summed across the turn's API steps (each step rereads the cached prefix),
-- so they price the turn but cannot measure context — reading them as
-- occupancy forced a rotation after almost any multi-step turn (#94).
-- Old rows stay 0: "unmeasured", never "empty".
ALTER TABLE turns ADD COLUMN context_tokens INTEGER NOT NULL DEFAULT 0;
