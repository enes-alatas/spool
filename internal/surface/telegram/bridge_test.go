package telegram

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// A group's ingest election only counts bots whose binding predates the
// message, so pollers racing on one message read the same candidate set.
func TestBoundBefore(t *testing.T) {
	const msgDate = 1_700_000_000 // seconds, Telegram's clock

	cases := []struct {
		name      string
		boundAtMS int64
		want      bool
	}{
		{"never recorded, so from before the rule existed", 0, true},
		{"bound well before the message", (msgDate - 3600) * 1000, true},
		{"bound just outside the settle margin", (msgDate - int64(defaultBindSettle.Seconds()) - 1) * 1000, true},
		{"bound inside the settle margin", (msgDate - 1) * 1000, false},
		{"bound while handling this very message", msgDate * 1000, false},
		{"bound after the message, catching up on a backlog", (msgDate + 60) * 1000, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := boundBefore(&store.Loop{TGGroupBoundAt: testCase.boundAtMS}, msgDate, defaultBindSettle)
			if got != testCase.want {
				t.Fatalf("boundBefore(bound_at=%d, date=%d) = %v, want %v",
					testCase.boundAtMS, msgDate, got, testCase.want)
			}
		})
	}
	if defaultBindSettle <= 0 {
		t.Fatalf("defaultBindSettle must be positive, got %v", time.Duration(defaultBindSettle))
	}
}

// laterCaptureStore answers OwnerDMChat with a chat that "DMed the bot"
// after a send was accepted — what the bridge would deliver to if it ever
// re-resolved instead of honoring the pinned chat. Every other store method
// panics via the embedded nil interfaces: delivery must not need them.
type laterCaptureStore struct{ store.Store }

func (laterCaptureStore) Messages() store.MessageStore { return laterCaptureMessages{} }

type laterCaptureMessages struct{ store.MessageStore }

func (laterCaptureMessages) OwnerDMChat(context.Context, string) (int64, error) {
	return 99, nil // chat B, the capture newer than the accepted send
}

// An owner_dm send accepted for one chat must be delivered to that chat even
// when another human's DM becomes the newest capture between send acceptance
// and bridge delivery (ADR-0025). The stub store reports the newer capture,
// so this fails if the bridge ever resolves OwnerDMChat again instead of
// using the chat route.Send pinned on the payload.
func TestOwnerDMDeliveryUsesPinnedChat(t *testing.T) {
	bot := &poller{loopID: "l1", sendCh: make(chan sendReq, 4)}
	br := &Bridge{
		store:   laterCaptureStore{},
		log:     slog.Default(),
		pollers: map[string]*poller{"l1": bot},
	}

	br.mirrorMessage(context.Background(), &route.MessagePayload{
		Message: store.Message{
			Origin:             store.OriginLoop,
			FromLoopID:         "l1",
			Conversation:       store.ConversationOwnerDM,
			ConversationLoopID: "l1",
			Text:               "answer for A",
		},
		OwnerDMChat: 42, // chat A, resolved when the send was accepted
	})

	select {
	case req := <-bot.sendCh:
		if req.chatID != 42 {
			t.Fatalf("owner_dm delivered to chat %d, want the pinned 42", req.chatID)
		}
	default:
		t.Fatal("owner_dm send was not delivered at all")
	}
}

// queueFullStore records what the bridge writes when it cannot queue a
// send. Every other store method panics via the embedded nil interfaces.
type queueFullStore struct {
	store.Store
	msgs   *queueFullMessages
	events *queueFullEvents
}

func (fake queueFullStore) Messages() store.MessageStore { return fake.msgs }
func (fake queueFullStore) Events() store.EventStore     { return fake.events }

type queueFullMessages struct {
	store.MessageStore
	failedID int64
	sendErr  string
}

func (messages *queueFullMessages) SetSendResult(_ context.Context, id, _ int64, sendErr string) error {
	messages.failedID, messages.sendErr = id, sendErr
	return nil
}

type queueFullEvents struct {
	store.EventStore
	got []*store.Event
}

func (events *queueFullEvents) Insert(_ context.Context, ev *store.Event) (int64, error) {
	events.got = append(events.got, ev)
	return int64(len(events.got)), nil
}

// A send whose bot queue has no room is a send failure, recorded where any
// other is: on the row and on the loop's timeline. Before, the first chunk
// was dropped with its recordFor, and the row read as in flight until the
// next restart blamed the restart for it.
func TestQueueFullIsASendFailure(t *testing.T) {
	bot := &poller{loopID: "l1", sendCh: make(chan sendReq)} // no room at all
	st := queueFullStore{msgs: &queueFullMessages{}, events: &queueFullEvents{}}
	br := &Bridge{
		store:   st,
		log:     slog.Default(),
		bus:     bus.New(),
		pollers: map[string]*poller{"l1": bot},
	}

	br.mirrorMessage(context.Background(), &route.MessagePayload{
		Message: store.Message{
			ID:                 7,
			Origin:             store.OriginLoop,
			FromLoopID:         "l1",
			Conversation:       store.ConversationOwnerDM,
			ConversationLoopID: "l1",
			Text:               "never queued",
			Mirror:             store.MirrorPending,
		},
		OwnerDMChat: 42,
	})

	if st.msgs.failedID != 7 || st.msgs.sendErr != errQueueFull {
		t.Fatalf("row failure = (%d, %q), want (7, %q)", st.msgs.failedID, st.msgs.sendErr, errQueueFull)
	}
	if len(st.events.got) != 1 || st.events.got[0].Subtype != "send_failed" {
		t.Fatalf("timeline events = %+v, want one send_failed", st.events.got)
	}
}

// TestExcerptCutsOnRuneBoundary pins the property that matters for a note the
// operator reads: the excerpt of a lost message is always valid UTF-8, whatever
// byte length the message happens to have. Turkish, because it is the language
// the note is most likely to be quoting.
func TestExcerptCutsOnRuneBoundary(t *testing.T) {
	const limit = 12
	body := strings.Repeat("çalışıyor ", 8)
	for pad := 0; pad < 16; pad++ {
		text := strings.Repeat("a", pad) + body
		got := excerpt(text, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("pad=%d: excerpt is not valid UTF-8: %q", pad, got)
		}
		if len(got) > limit+len("…") {
			t.Fatalf("pad=%d: excerpt %q exceeds the cap", pad, got)
		}
		if !strings.HasPrefix(text, strings.TrimSuffix(got, "…")) {
			t.Fatalf("pad=%d: excerpt %q is not the opening of the message", pad, got)
		}
	}
}

// loginStore serves loop rows by id and records timeline events. Every other
// store method panics via the embedded nil interfaces.
type loginStore struct {
	store.Store
	loops  loginLoops
	events *queueFullEvents
}

func (fake loginStore) Loops() store.LoopStore   { return fake.loops }
func (fake loginStore) Events() store.EventStore { return fake.events }

type loginLoops struct {
	store.LoopStore
	rows map[string]*store.Loop
}

func (loops loginLoops) Get(_ context.Context, id string) (*store.Loop, error) {
	return loops.rows[id], nil
}

// One refused login stops every loop that shares it, each at its own next
// turn, and the owner hears about it once (#419): from the first loop that
// can reach them privately, with the all-clear in the same chat, whichever
// loop gets through first. A loop that cannot reach them leaves the telling
// to the next, and a login that fails separately is a separate notice.
func TestLoginNoticeOncePerOwnerPerOutage(t *testing.T) {
	const owner, chat = 6100, 6100
	bots := map[string]*poller{}
	rows := map[string]*store.Loop{}
	for _, id := range []string{"nochat", "alpha", "beta", "boxed"} {
		bots[id] = &poller{loopID: id, sendCh: make(chan sendReq, 4)}
		rows[id] = &store.Loop{ID: id, Name: id, TGBotToken: id, OwnerTGUserID: owner, OwnerDMChatID: chat}
	}
	rows["nochat"].OwnerDMChatID = 0
	st := loginStore{loops: loginLoops{rows: rows}, events: &queueFullEvents{}}
	br := &Bridge{store: st, log: slog.Default(), bus: bus.New(), pollers: bots,
		loginTold: map[loginOutage]loginTeller{}}

	refused := func(id string, host bool) {
		br.noticeLogin(context.Background(), &surface.LoginNotice{
			LoopID: id, LoopName: id, Refused: true, Sentence: "OAuth session expired", HostLogin: host})
	}
	works := func(id string, host bool) {
		br.noticeLogin(context.Background(), &surface.LoginNotice{LoopID: id, LoopName: id, HostLogin: host})
	}
	sent := func() map[string]string {
		got := map[string]string{}
		for id, bot := range bots {
			select {
			case req := <-bot.sendCh:
				if req.chatID != chat {
					t.Fatalf("%s's notice went to chat %d, want the owner's %d", id, req.chatID, chat)
				}
				got[id] = req.text
			default:
			}
		}
		return got
	}

	refused("nochat", true)
	if got := sent(); len(got) != 0 {
		t.Fatalf("a loop with no private chat sent %v", got)
	}
	if n := len(st.events.got); n != 1 || !strings.Contains(st.events.got[0].Payload, `"sent":false`) {
		t.Fatalf("timeline events = %+v, want one unsent owner_notice", st.events.got)
	}

	refused("alpha", true)
	refused("beta", true)
	got := sent()
	if len(got) != 1 || !strings.Contains(got["alpha"], "alpha has stopped") ||
		!strings.Contains(got["alpha"], "Every bare loop") {
		t.Fatalf("after two refusals of the host login: sent %v, want alpha's notice alone", got)
	}

	refused("boxed", false)
	if got := sent(); len(got) != 1 || !strings.Contains(got["boxed"], "setup-token") {
		t.Fatalf("a refused setup-token beside a refused host login: sent %v, want boxed's own notice", got)
	}

	works("beta", true)
	works("nochat", true)
	if got := sent(); len(got) != 1 || !strings.Contains(got["alpha"], "works again") {
		t.Fatalf("after the host login worked again: sent %v, want one all-clear in alpha's chat", got)
	}
	refused("beta", true)
	if got := sent(); len(got) != 1 || got["beta"] == "" {
		t.Fatalf("a refusal after the all-clear is a new outage: sent %v, want beta's notice", got)
	}
}
