package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// polls is the ballots the hub's poll messages carry, and their votes
// (ADR-0041).
type polls struct{ db *sql.DB }

const pollCols = `p.message_id, p.options, p.multiple, p.closes_at, p.closed_at, p.close_told_at`

const voteCols = `v.id, v.poll_id, v.voter_key, v.voter, v.choice, v.ts, v.told_at`

func scanPolls(rows *sql.Rows, err error) ([]*store.Poll, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.Poll{}
	for rows.Next() {
		var poll store.Poll
		var options string
		if err := rows.Scan(&poll.MessageID, &options, &poll.Multiple, &poll.ClosesAt, &poll.ClosedAt,
			&poll.CloseToldAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(options), &poll.Options); err != nil {
			return nil, err
		}
		out = append(out, &poll)
	}
	return out, rows.Err()
}

func scanVotes(rows *sql.Rows, err error) ([]*store.Vote, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*store.Vote{}
	for rows.Next() {
		var vote store.Vote
		var choice string
		if err := rows.Scan(&vote.ID, &vote.PollID, &vote.VoterKey, &vote.Voter, &choice, &vote.TS,
			&vote.ToldAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(choice), &vote.Choice); err != nil {
			return nil, err
		}
		out = append(out, &vote)
	}
	return out, rows.Err()
}

// placeholders is "?,?,…" for an IN list of ids, and the ids as arguments
// after any leading ones.
func placeholders(ids []int64, leading ...any) (string, []any) {
	args := leading
	for _, id := range ids {
		args = append(args, id)
	}
	return strings.Repeat(",?", len(ids))[1:], args
}

func (table polls) Create(ctx context.Context, poll *store.Poll) error {
	options, err := json.Marshal(poll.Options)
	if err != nil {
		return err
	}
	_, err = table.db.ExecContext(ctx, `INSERT INTO polls (message_id, options, multiple, closes_at)
		VALUES (?,?,?,?)`, poll.MessageID, string(options), poll.Multiple, poll.ClosesAt)
	switch {
	case err == nil:
		return nil
	case strings.Contains(err.Error(), "FOREIGN KEY"):
		return store.ErrNotFound
	case strings.Contains(err.Error(), "UNIQUE"):
		return store.ErrDuplicate
	}
	return err
}

func (table polls) Get(ctx context.Context, messageID int64) (*store.Poll, error) {
	got, err := scanPolls(table.db.QueryContext(ctx, `SELECT `+pollCols+` FROM polls AS p WHERE p.message_id=?`,
		messageID))
	if err != nil {
		return nil, err
	}
	if len(got) == 0 {
		return nil, store.ErrNotFound
	}
	return got[0], nil
}

func (table polls) ListByMessages(ctx context.Context, messageIDs []int64) ([]*store.Poll, error) {
	if len(messageIDs) == 0 {
		return []*store.Poll{}, nil
	}
	in, args := placeholders(messageIDs)
	return scanPolls(table.db.QueryContext(ctx, `SELECT `+pollCols+` FROM polls AS p
		WHERE p.message_id IN (`+in+`) ORDER BY p.message_id`, args...))
}

// Vote replaces the voter's row in one statement that only an open poll
// lets through, so a vote racing the close either lands before it or is
// refused. A choice equal to the one recorded changes nothing, so the
// author is not told the same choice twice.
func (table polls) Vote(ctx context.Context, vote *store.Vote) (bool, error) {
	if vote.Choice == nil {
		vote.Choice = []int{}
	}
	choice, err := json.Marshal(vote.Choice)
	if err != nil {
		return false, err
	}
	res, err := table.db.ExecContext(ctx, `INSERT INTO votes (poll_id, voter_key, voter, choice, ts)
		SELECT ?,?,?,?,? FROM polls WHERE message_id=? AND closed_at=0
		ON CONFLICT (poll_id, voter_key) DO UPDATE
		SET voter=excluded.voter, choice=excluded.choice, ts=excluded.ts, told_at=0
		WHERE votes.choice <> excluded.choice`,
		vote.PollID, vote.VoterKey, vote.Voter, string(choice), vote.TS, vote.PollID)
	if err != nil {
		return false, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed > 0 {
		return true, table.db.QueryRowContext(ctx, `SELECT id FROM votes WHERE poll_id=? AND voter_key=?`,
			vote.PollID, vote.VoterKey).Scan(&vote.ID)
	}
	poll, err := table.Get(ctx, vote.PollID)
	if err != nil {
		return false, err
	}
	if poll.ClosedAt != 0 {
		return false, store.ErrPollClosed
	}
	return false, nil
}

func (table polls) Votes(ctx context.Context, pollIDs []int64) ([]*store.Vote, error) {
	if len(pollIDs) == 0 {
		return []*store.Vote{}, nil
	}
	in, args := placeholders(pollIDs)
	return scanVotes(table.db.QueryContext(ctx, `SELECT `+voteCols+` FROM votes AS v
		WHERE v.poll_id IN (`+in+`) ORDER BY v.ts, v.id`, args...))
}

func (table polls) Close(ctx context.Context, messageID, closedAt int64) (bool, error) {
	res, err := table.db.ExecContext(ctx, `UPDATE polls SET closed_at=? WHERE message_id=? AND closed_at=0`,
		closedAt, messageID)
	if err != nil {
		return false, err
	}
	closed, err := res.RowsAffected()
	return closed > 0, err
}

func (table polls) Due(ctx context.Context, now int64) ([]*store.Poll, error) {
	return scanPolls(table.db.QueryContext(ctx, `SELECT `+pollCols+` FROM polls AS p
		WHERE p.closed_at=0 AND p.closes_at>0 AND p.closes_at<=? ORDER BY p.closes_at, p.message_id`, now))
}

func (table polls) UntoldVotes(ctx context.Context, loopID string) ([]*store.Vote, error) {
	return scanVotes(table.db.QueryContext(ctx, `SELECT `+voteCols+` FROM votes AS v
		JOIN messages AS m ON m.id = v.poll_id
		WHERE m.from_loop_id=? AND v.voter_key<>? AND v.told_at=0 ORDER BY v.ts, v.id`,
		loopID, store.LoopReactor(loopID)))
}

func (table polls) MarkVotesTold(ctx context.Context, ids []int64, toldAt int64) error {
	if len(ids) == 0 {
		return nil
	}
	in, args := placeholders(ids, toldAt)
	_, err := table.db.ExecContext(ctx, `UPDATE votes SET told_at=? WHERE id IN (`+in+`)`, args...)
	return err
}

func (table polls) UntoldCloses(ctx context.Context, loopID string) ([]*store.Poll, error) {
	return scanPolls(table.db.QueryContext(ctx, `SELECT `+pollCols+` FROM polls AS p
		JOIN messages AS m ON m.id = p.message_id
		WHERE m.from_loop_id=? AND p.closed_at>0 AND p.close_told_at=0 ORDER BY p.closed_at, p.message_id`,
		loopID))
}

func (table polls) MarkClosesTold(ctx context.Context, messageIDs []int64, toldAt int64) error {
	if len(messageIDs) == 0 {
		return nil
	}
	in, args := placeholders(messageIDs, toldAt)
	_, err := table.db.ExecContext(ctx, `UPDATE polls SET close_told_at=? WHERE message_id IN (`+in+`)`, args...)
	return err
}
