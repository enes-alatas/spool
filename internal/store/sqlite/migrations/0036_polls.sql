-- Polls (ADR-0041): a poll is a message with a ballot, kept beside it. The
-- options are a JSON array, in order. closes_at is when the hub closes it,
-- 0 for a poll its author closes; closed_at is when it closed, 0 while it
-- is open. close_told_at is when the author was told the result, 0 until
-- then: the close rides with its next turn rather than waking it.
CREATE TABLE polls (
    message_id    INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    options       TEXT NOT NULL,
    multiple      INTEGER NOT NULL DEFAULT 0,
    closes_at     INTEGER NOT NULL DEFAULT 0,
    closed_at     INTEGER NOT NULL DEFAULT 0,
    close_told_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX polls_closes_at ON polls (closes_at) WHERE closed_at = 0 AND closes_at > 0;

-- A vote is one voter's whole choice in one poll: the option indexes, a
-- JSON array, [] once retracted. The voter is keyed as a reactor is
-- ('loop:<id>' or '<surface>:<user id>'), and a new vote replaces the
-- voter's row. told_at is when the poll's author was told of the choice as
-- it now stands, 0 until then.
CREATE TABLE votes (
    id        INTEGER PRIMARY KEY,
    poll_id   INTEGER NOT NULL REFERENCES polls(message_id) ON DELETE CASCADE,
    voter_key TEXT NOT NULL,
    voter     TEXT NOT NULL DEFAULT '',
    choice    TEXT NOT NULL,
    ts        INTEGER NOT NULL,
    told_at   INTEGER NOT NULL DEFAULT 0,
    UNIQUE (poll_id, voter_key)
);
