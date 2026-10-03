package telegram

import (
	"context"
	"log/slog"
	"testing"

	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// pollStore holds one poll, message 7, which l1's bot sent to chat -100 as
// its message 70 and Telegram knows as "p7".
type pollStore struct {
	store.Store
	poll *store.Poll
}

func (fake *pollStore) Polls() store.PollStore       { return pollRows{fake: fake} }
func (fake *pollStore) Messages() store.MessageStore { return pollMessages{} }
func (fake *pollStore) Loops() store.LoopStore       { return telegramLoops{} }

type pollRows struct {
	store.PollStore
	fake *pollStore
}

func (rows pollRows) Get(_ context.Context, messageID int64) (*store.Poll, error) {
	if messageID != rows.fake.poll.MessageID {
		return nil, store.ErrNotFound
	}
	return rows.fake.poll, nil
}

func (rows pollRows) ByTGPollID(_ context.Context, id string) (*store.Poll, error) {
	if id != "p7" {
		return nil, store.ErrNotFound
	}
	return rows.fake.poll, nil
}

type pollMessages struct{ store.MessageStore }

func (pollMessages) Get(_ context.Context, id int64) (*store.Message, error) {
	return &store.Message{ID: id, FromLoopID: "l1"}, nil
}

func (pollMessages) Ref(_ context.Context, messageID int64, botLoopID string) (*store.SurfaceRef, error) {
	if messageID != 7 || botLoopID != "l1" {
		return nil, store.ErrNotFound
	}
	return &store.SurfaceRef{MessageID: 7, BotLoopID: "l1", TGChatID: -100, TGMessageID: 70}, nil
}

func pollBridge() (*Bridge, *poller, *pollStore) {
	bot := &poller{loopID: "l1", name: "alpha", sendCh: make(chan sendReq, 4)}
	fake := &pollStore{poll: &store.Poll{MessageID: 7, Options: []string{"yes", "no"}, TGPollID: "p7"}}
	return withLedger(&Bridge{store: fake, log: slog.Default(), pollers: map[string]*poller{"l1": bot}}), bot, fake
}

func queued(t *testing.T, bot *poller, what string) sendReq {
	t.Helper()
	select {
	case req := <-bot.sendCh:
		return req
	default:
		t.Fatalf("%s: nothing queued", what)
		return sendReq{}
	}
}

func nothingQueued(t *testing.T, bot *poller, what string) {
	t.Helper()
	select {
	case req := <-bot.sendCh:
		t.Fatalf("%s: queued %+v", what, req)
	default:
	}
}

// A loop's message that carries a ballot goes out as a native poll, its
// words the question; one that carries none is a post.
func TestAPollGoesOutAsANativePoll(t *testing.T) {
	br, bot, _ := pollBridge()
	send := func(id int64) sendReq {
		br.mirrorMessage(context.Background(), &route.MessagePayload{
			Message: store.Message{ID: id, Origin: store.OriginLoop, FromLoopID: "l1",
				Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1", Text: "ship friday?"},
			OwnerDMChat: 42,
		})
		return queued(t, bot, "a loop's send")
	}
	if req := send(7); req.poll == nil || req.poll.MessageID != 7 || req.text != "ship friday?" || req.chatID != 42 {
		t.Fatalf("the poll queued %+v, want ballot 7 asking %q in 42", req, "ship friday?")
	}
	if req := send(8); req.poll != nil {
		t.Fatalf("a message with no ballot queued as a poll: %+v", req)
	}
}

// The hub's close stops the poll where the loop's bot sent it; a vote does
// not touch Telegram's count.
func TestTheCloseStopsThePollOnTelegram(t *testing.T) {
	br, bot, _ := pollBridge()
	br.mirrorPoll(context.Background(), &route.PollPayload{PollID: 7, Vote: &store.Vote{PollID: 7}})
	nothingQueued(t, bot, "a vote")
	br.mirrorPoll(context.Background(), &route.PollPayload{PollID: 7, Closed: true})
	if req := queued(t, bot, "the close"); req.stopPollAt != 70 || req.chatID != -100 {
		t.Fatalf("the close queued %+v, want a stop of 70 in -100", req)
	}
	br.mirrorPoll(context.Background(), &route.PollPayload{PollID: 8, Closed: true})
	nothingQueued(t, bot, "the close of a poll the bot never sent")
}

// A vote in a poll the hub has closed means the bot missed the close, so
// it stops the poll then; a vote in a poll the hub never sent is nobody's.
func TestAVoteAfterTheCloseStopsThePoll(t *testing.T) {
	br, bot, fake := pollBridge()
	br.router = route.New(fake, nil, nil, nil) // never reached: neither vote is recorded
	voter := &User{ID: 42, FirstName: "Enes"}

	br.handlePollAnswer(context.Background(), bot, &PollAnswer{PollID: "p9", User: voter, OptionIDs: []int{0}})
	nothingQueued(t, bot, "a vote in a poll the hub never sent")

	fake.poll.ClosedAt = 100
	br.handlePollAnswer(context.Background(), bot, &PollAnswer{PollID: "p7", User: voter, OptionIDs: []int{0}})
	if req := queued(t, bot, "a vote after the close"); req.stopPollAt != 70 || req.chatID != -100 {
		t.Fatalf("a vote after the close queued %+v, want a stop of 70 in -100", req)
	}
}
