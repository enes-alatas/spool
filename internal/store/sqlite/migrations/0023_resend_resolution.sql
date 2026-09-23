-- A loop told its words never arrived says them again, and until now that
-- resend was an unrelated new message: the failed row stayed unresolved and
-- the operator had to dismiss by hand a failure the loop had already dealt
-- with (#270). send_message gains a `resends` field, and a send that carries
-- one resolves the row it names when it gets through.
--
-- resends_id is that claim, on the new message: which failure these words
-- were said again for. It is stored rather than carried with the send in
-- flight because the resend can fail too, and then the claim has to outlive
-- it — the loop is told about the new failure, resends that, and when the
-- words finally land every failure in the chain is resolved at once. Carried
-- only in memory, the first link would be orphaned: already marked told, so
-- never named to the loop again, and unresolvable by anyone but a human.
ALTER TABLE messages ADD COLUMN resends_id INTEGER NOT NULL DEFAULT 0;

-- send_resent_as is which message carried the words the second time. The
-- reason alone would say a resend happened; the id says which one, so an
-- operator reading the failure can read what was said instead of taking it
-- on trust. Zero for every other resolution. On a chain it is the message
-- that finally got through, not the next link: what an operator wants from a
-- resolved failure is the words that arrived.
ALTER TABLE messages ADD COLUMN send_resent_as INTEGER NOT NULL DEFAULT 0;
