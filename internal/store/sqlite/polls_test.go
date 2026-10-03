package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func choices(votes []*store.Vote) []string {
	out := []string{}
	for _, vote := range votes {
		out = append(out, fmt.Sprintf("%s %v", vote.VoterKey, vote.Choice))
	}
	return out
}

func createPoll(t *testing.T, db *DB, fromLoopID string, closesAt int64) *store.Poll {
	t.Helper()
	message := insertMessage(t, db, &store.Message{TS: 1, Origin: store.OriginLoop, FromLoopID: fromLoopID,
		Text: "ship friday?"})
	poll := &store.Poll{MessageID: message.ID, Options: []string{"yes", "no", "next week"}, ClosesAt: closesAt}
	if err := db.Polls().Create(context.Background(), poll); err != nil {
		t.Fatal(err)
	}
	return poll
}

// A message carries one ballot, and only a message the hub holds can.
func TestAPollIsABallotBesideItsMessage(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	poll := createPoll(t, db, "l1", 0)

	got, err := db.Polls().Get(ctx, poll.MessageID)
	if err != nil || len(got.Options) != 3 || got.Options[2] != "next week" || got.Multiple || got.ClosedAt != 0 {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if err := db.Polls().Create(ctx, &store.Poll{MessageID: poll.MessageID, Options: []string{"a", "b"}}); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("a second ballot on one message: err=%v, want ErrDuplicate", err)
	}
	if err := db.Polls().Create(ctx, &store.Poll{MessageID: 9999, Options: []string{"a", "b"}}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a ballot on no message: err=%v, want ErrNotFound", err)
	}
	if _, err := db.Polls().Get(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get of no poll: err=%v, want ErrNotFound", err)
	}
	listed, err := db.Polls().ListByMessages(ctx, []int64{poll.MessageID, 9999})
	if err != nil || len(listed) != 1 || listed[0].MessageID != poll.MessageID {
		t.Errorf("ListByMessages = %+v, %v", listed, err)
	}
}

// A vote is the voter's whole choice: a new one replaces it, the same one
// changes nothing, an empty one retracts it, and a closed poll takes none.
func TestAVoteReplacesTheVotersChoice(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	poll := createPoll(t, db, "l1", 0)
	person := store.PersonReactor(store.SurfaceTelegram, "42")
	vote := func(choice ...int) (bool, error) {
		return db.Polls().Vote(ctx, &store.Vote{PollID: poll.MessageID, VoterKey: person, Voter: "enes",
			Choice: choice, TS: 10})
	}

	for i, step := range []struct {
		choice  []int
		changed bool
	}{{[]int{0}, true}, {[]int{0}, false}, {[]int{2}, true}, {nil, true}} {
		if changed, err := vote(step.choice...); err != nil || changed != step.changed {
			t.Fatalf("vote %d %v: changed=%v err=%v, want changed=%v", i+1, step.choice, changed, err, step.changed)
		}
	}
	if _, err := db.Polls().Vote(ctx, &store.Vote{PollID: poll.MessageID, VoterKey: store.LoopReactor("l2"),
		Voter: "beta", Choice: []int{1}, TS: 11}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Polls().Votes(ctx, []int64{poll.MessageID})
	if want := fmt.Sprint([]string{person + " []", "loop:l2 [1]"}); err != nil || fmt.Sprint(choices(got)) != want {
		t.Fatalf("Votes = %v, %v; want %s", choices(got), err, want)
	}

	if closed, err := db.Polls().Close(ctx, poll.MessageID, 20); err != nil || !closed {
		t.Fatalf("Close = %v, %v", closed, err)
	}
	if closed, err := db.Polls().Close(ctx, poll.MessageID, 21); err != nil || closed {
		t.Errorf("closing a closed poll: closed=%v err=%v, want neither", closed, err)
	}
	if _, err := vote(1); !errors.Is(err, store.ErrPollClosed) {
		t.Errorf("a vote after the close: err=%v, want ErrPollClosed", err)
	}
	if _, err := db.Polls().Vote(ctx, &store.Vote{PollID: 9999, VoterKey: person, Choice: []int{0}}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a vote in no poll: err=%v, want ErrNotFound", err)
	}
}

// The author is told each voter's choice as it stands, once, and never its
// own; a choice changed after it was told is news again. The result of a
// closed poll is told once too.
func TestThePollsAuthorIsToldEachChoiceAndTheResultOnce(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	poll := createPoll(t, db, "l1", 0)
	other := createPoll(t, db, "l2", 0)
	vote := func(pollID int64, voterKey string, choice ...int) {
		t.Helper()
		if _, err := db.Polls().Vote(ctx, &store.Vote{PollID: pollID, VoterKey: voterKey, Choice: choice, TS: 10}); err != nil {
			t.Fatal(err)
		}
	}
	person := store.PersonReactor(store.SurfaceSlack, "U1")
	vote(poll.MessageID, person, 0)
	vote(poll.MessageID, store.LoopReactor("l1"), 1) // its own: never news
	vote(other.MessageID, person, 1)                 // another loop's poll

	untold, err := db.Polls().UntoldVotes(ctx, "l1")
	if err != nil || fmt.Sprint(choices(untold)) != fmt.Sprint([]string{person + " [0]"}) {
		t.Fatalf("UntoldVotes = %v, %v", choices(untold), err)
	}
	if err := db.Polls().MarkVotesTold(ctx, []int64{untold[0].ID}, 30); err != nil {
		t.Fatal(err)
	}
	if untold, err := db.Polls().UntoldVotes(ctx, "l1"); err != nil || len(untold) != 0 {
		t.Fatalf("after MarkVotesTold: %v, %v", choices(untold), err)
	}
	vote(poll.MessageID, person, 2)
	if untold, err := db.Polls().UntoldVotes(ctx, "l1"); err != nil || fmt.Sprint(choices(untold)) != fmt.Sprint([]string{person + " [2]"}) {
		t.Fatalf("a changed choice: UntoldVotes = %v, %v", choices(untold), err)
	}

	if closes, err := db.Polls().UntoldCloses(ctx, "l1"); err != nil || len(closes) != 0 {
		t.Fatalf("an open poll's result: %+v, %v", closes, err)
	}
	if _, err := db.Polls().Close(ctx, poll.MessageID, 40); err != nil {
		t.Fatal(err)
	}
	closes, err := db.Polls().UntoldCloses(ctx, "l1")
	if err != nil || len(closes) != 1 || closes[0].MessageID != poll.MessageID {
		t.Fatalf("UntoldCloses = %+v, %v", closes, err)
	}
	if err := db.Polls().MarkClosesTold(ctx, []int64{poll.MessageID}, 41); err != nil {
		t.Fatal(err)
	}
	if closes, err := db.Polls().UntoldCloses(ctx, "l1"); err != nil || len(closes) != 0 {
		t.Fatalf("after MarkClosesTold: %+v, %v", closes, err)
	}
}

// Due finds the open polls whose time has come, and none that closes by
// hand or has closed. Deleting a poll's message deletes its ballot and votes.
func TestDuePollsAndDeletion(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	due := createPoll(t, db, "l1", 100)
	later := createPoll(t, db, "l1", 300)
	byHand := createPoll(t, db, "l1", 0)
	closed := createPoll(t, db, "l1", 50)
	if _, err := db.Polls().Close(ctx, closed.MessageID, 60); err != nil {
		t.Fatal(err)
	}
	got, err := db.Polls().Due(ctx, 200)
	if err != nil || len(got) != 1 || got[0].MessageID != due.MessageID {
		t.Fatalf("Due(200) = %+v, %v; want only %d (not %d, %d or %d)", got, err, due.MessageID,
			later.MessageID, byHand.MessageID, closed.MessageID)
	}

	if _, err := db.Polls().Vote(ctx, &store.Vote{PollID: due.MessageID, VoterKey: "loop:l2", Choice: []int{0}, TS: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM messages WHERE id=?`, due.MessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Polls().Get(ctx, due.MessageID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the ballot outlived its message: err=%v", err)
	}
	if votes, err := db.Polls().Votes(ctx, []int64{due.MessageID}); err != nil || len(votes) != 0 {
		t.Errorf("the votes outlived their poll: %v, %v", choices(votes), err)
	}
}

// Telegram reports a vote by its own poll id alone, so the id the bot was
// given finds the poll, and an id no bot was given finds none.
func TestAPollIsFoundByItsTelegramID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	poll := createPoll(t, db, "l1", 0)
	if _, err := db.Polls().ByTGPollID(ctx, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unsent poll found by the empty id: err=%v", err)
	}
	if err := db.Polls().SetTGPollID(ctx, poll.MessageID, "5"); err != nil {
		t.Fatal(err)
	}
	got, err := db.Polls().ByTGPollID(ctx, "5")
	if err != nil || got.MessageID != poll.MessageID || got.TGPollID != "5" {
		t.Fatalf("ByTGPollID = %+v, %v", got, err)
	}
	if _, err := db.Polls().ByTGPollID(ctx, "6"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an id no bot was given: err=%v", err)
	}
}
