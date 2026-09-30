package telegram

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// deliveries records what the router hands each loop.
type deliveries struct {
	mu  sync.Mutex
	got map[string][]loop.Envelope
}

func (d *deliveries) Deliver(loopID string, env loop.Envelope) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.got[loopID] = append(d.got[loopID], env)
	return true
}

func (d *deliveries) count(loopID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.got[loopID])
}

// In a basic group the operator's reply to beta's post reaches alpha's bot,
// the elected ingester, with nothing embedded, and beta's bot with the post
// it answers (#424). The two handle it concurrently, in either order, and
// either way the stored reply names its target and beta is delivered it
// exactly once.
func TestReplyTargetOnlyTheAuthorsBotSaw(t *testing.T) {
	const chat = -4242
	for _, order := range []string{"author first", "ingester first"} {
		t.Run(order, func(t *testing.T) {
			ctx := context.Background()
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			for _, id := range []string{"loop_a", "loop_b"} {
				if err := db.Loops().Create(ctx, &store.Loop{ID: id, Name: id, Status: store.StatusActive, WorkspaceMode: "none", Pacing: "fixed", Runtime: store.RuntimeBare,
					TGBotToken: id, TGBotUsername: id + "_bot", TGGroupChatID: chat}); err != nil {
					t.Fatal(err)
				}
			}
			post := &store.Message{Origin: store.OriginLoop, Author: "loop_b", FromLoopID: "loop_b",
				Text: "approved, please merge", Conversation: store.ConversationGroup}
			if err := db.Messages().Insert(ctx, post); err != nil {
				t.Fatal(err)
			}
			if err := db.Messages().PutRef(ctx, &store.SurfaceRef{MessageID: post.ID, BotLoopID: "loop_b",
				TGChatID: chat, TGMessageID: 667}); err != nil {
				t.Fatal(err)
			}

			delivered := &deliveries{got: map[string][]loop.Envelope{}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			alpha := &poller{loopID: "loop_a", name: "loop_a"}
			beta := &poller{loopID: "loop_b", name: "loop_b"}
			br := &Bridge{store: db, bus: bus.New(), router: route.New(db, bus.New(), delivered, log), log: log,
				bindSettle: defaultBindSettle, dedup: newDedupLRU(dedupSize),
				pollers: map[string]*poller{"loop_a": alpha, "loop_b": beta}}

			operator := &User{ID: 77, FirstName: "Operator", Username: "operator"}
			date := time.Now().Unix()
			reply := func(id int64, embedded *Message) *Message {
				return &Message{MessageID: id, From: operator, Date: date, Text: "it seems out of date",
					Chat: Chat{ID: chat, Type: "group"}, ReplyToMessage: embedded}
			}
			seenByBeta := reply(668, &Message{MessageID: 667, From: &User{ID: 9, IsBot: true, Username: "loop_b_bot"},
				Date: date, Text: "↳ re loop_a: …\n\napproved, please merge", Chat: Chat{ID: chat, Type: "group"}})
			seenByAlpha := reply(546, nil)
			handle := func(bot *poller, message *Message) {
				target := br.ownReplyTarget(ctx, bot, message)
				br.recordSighting(ctx, bot, message, target)
				br.ingestGroupMessage(ctx, bot, message, "operator", message.Text, nil, target)
			}
			if order == "author first" {
				handle(beta, seenByBeta)
				handle(alpha, seenByAlpha)
			} else {
				handle(alpha, seenByAlpha)
				handle(beta, seenByBeta)
			}

			stored, err := db.Messages().ByTGKey(ctx, tgKey(seenByAlpha))
			if err != nil {
				t.Fatal(err)
			}
			if stored.ReplyToID != post.ID {
				t.Fatalf("reply_to_id = %d, want beta's post %d", stored.ReplyToID, post.ID)
			}
			if len(stored.DeliveredTo) != 1 || stored.DeliveredTo[0] != "loop_b" {
				t.Fatalf("delivered_to = %v, want loop_b alone", stored.DeliveredTo)
			}
			if n, other := delivered.count("loop_b"), delivered.count("loop_a"); n != 1 || other != 0 {
				t.Fatalf("delivered to beta %d times and alpha %d, want once and never", n, other)
			}
		})
	}
}
