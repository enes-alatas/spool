package slack

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

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

// inboundFixture is an adapter over a real store with two Slack loops, terra
// and milo, and one allowed sender, alice. users.info answers from a stand-in
// Web API.
func inboundFixture(t *testing.T) (*Adapter, store.Store, *deliveries) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, name := range []string{"terra", "milo"} {
		if err := db.Loops().Create(ctx, &store.Loop{ID: "loop_" + name, Name: name, Status: store.StatusActive,
			WorkspaceMode: "none", Pacing: "fixed", Runtime: store.RuntimeBare}); err != nil {
			t.Fatal(err)
		}
		botUser := "U0" + name
		if _, err := db.Loops().Edit(ctx, "loop_"+name, store.LoopEdit{Slack: &store.SlackIdentity{
			AppToken: "xapp-synthetic-" + name, BotToken: "xoxb-synthetic-" + name,
			BotUserID: botUser, BotName: name, TeamID: "T0ACME", TeamName: "Acme"}, UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SlackSenders().Create(ctx, &store.SlackSender{SlackUserID: "U0ALICE", TeamID: "T0ACME",
		Username: "alice", Display: "Alice", Status: store.SenderAllowed, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{
			"id": r.FormValue("user"), "team_id": "T0ACME", "name": "bob",
			"profile": map[string]any{"display_name": "Bob"}}})
	}))
	t.Cleanup(web.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	got := &deliveries{got: map[string][]loop.Envelope{}}
	publisher := bus.New()
	adapter := New(db, publisher, route.New(db, publisher, got, log), log, web.URL)
	return adapter, db, got
}

func channelEvent(user, text, ts string) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{"team_id": "T0ACME", "event": map[string]any{
		"type": "message", "channel_type": "channel", "channel": "C0FLEET", "user": user, "text": text, "ts": ts}})
	return payload
}

// Slack writes a mention as the user's id in brackets. A loop's app becomes
// @ and the loop's name, which is what the router addresses by; a sender
// Spool knows becomes their handle; everything else reads as Slack shows it.
func TestReadableMarkup(t *testing.T) {
	adapter, _, _ := inboundFixture(t)
	cases := []struct{ in, want string }{
		{"<@U0terra> status?", "@terra status?"},
		{"<@U0terra|terra> and <@U0milo>", "@terra and @milo"},
		{"ask <@U0ALICE>", "ask @alice"},
		{"ask <@U0NOBODY|carol>", "ask @carol"},
		{"ask <@U0NOBODY>", "ask @U0NOBODY"},
		{"in <#C0FLEET|fleet>", "in #fleet"},
		{"<!here> heads up", "@here heads up"},
		{"<!subteam^S0DEV|@devs> review", "@devs review"},
		{"see <https://example.com/pr/1|the PR>", "see the PR (https://example.com/pr/1)"},
		{"see <https://example.com/pr/1>", "see https://example.com/pr/1"},
		{"a &lt;b&gt; &amp;&amp; c", "a <b> && c"},
		{"&lt;@U0terra&gt;", "<@U0terra>"},
	}
	for _, testCase := range cases {
		if got := adapter.readable(context.Background(), testCase.in); got != testCase.want {
			t.Errorf("readable(%q) = %q, want %q", testCase.in, got, testCase.want)
		}
	}
}

// Both loops' apps are in the channel, so both hear every message in it, and
// Slack gives both the same ts. One row is stored and each mentioned loop is
// delivered it once, however the two links interleave (ADR-0020).
func TestTwoAppsInOneChannelIngestOnce(t *testing.T) {
	adapter, db, got := inboundFixture(t)
	event := channelEvent("U0ALICE", "<@U0terra> <@U0milo> status please", "1727600000.000100")
	var wg sync.WaitGroup
	for _, loopID := range []string{"loop_terra", "loop_milo"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adapter.ingest(context.Background(), &link{loopID: loopID}, event)
		}()
	}
	wg.Wait()
	msgs, err := db.Messages().List(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("stored %d rows, want 1", len(msgs))
	}
	if msgs[0].Text != "@terra @milo status please" || msgs[0].Author != "alice" ||
		msgs[0].Origin != store.OriginSlackChannel || msgs[0].Conversation != store.ConversationGroup {
		t.Errorf("stored %+v", msgs[0])
	}
	if got.count("loop_terra") != 1 || got.count("loop_milo") != 1 {
		t.Errorf("deliveries: terra %d, milo %d; want 1 each", got.count("loop_terra"), got.count("loop_milo"))
	}
}

// What no human said is never ingested: a bot's post, which is how every
// loop's own mirrored words come back to the other apps, an edit, and the
// app's own user.
func TestIngestDropsWhatNoHumanSaid(t *testing.T) {
	adapter, db, _ := inboundFixture(t)
	for _, event := range []map[string]any{
		{"type": "message", "channel_type": "channel", "channel": "C0FLEET", "user": "U0milo", "bot_id": "B0MILO", "text": "@terra hi", "ts": "1.1"},
		{"type": "message", "subtype": "message_changed", "channel_type": "channel", "channel": "C0FLEET", "text": "@terra hi", "ts": "1.2"},
		{"type": "message", "channel_type": "channel", "channel": "C0FLEET", "user": "U0terra", "text": "@terra hi", "ts": "1.3"},
		{"type": "message", "channel_type": "channel", "channel": "C0FLEET", "user": "U0ALICE", "text": "  ", "ts": "1.4"},
	} {
		payload, _ := json.Marshal(map[string]any{"team_id": "T0ACME", "event": event})
		adapter.ingest(context.Background(), &link{loopID: "loop_terra"}, payload)
	}
	if msgs, _ := db.Messages().List(context.Background(), 10); len(msgs) != 0 {
		t.Fatalf("stored %d rows from no human, want none: %+v", len(msgs), msgs[0])
	}
}

// A loop's app hears one channel: the first an allowed sender speaks in
// binds it, and a message from any other is counted rather than ingested.
func TestAppHearsTheChannelItBound(t *testing.T) {
	adapter, db, _ := inboundFixture(t)
	ctx := context.Background()
	heard := &link{loopID: "loop_terra"}
	adapter.ingest(ctx, heard, channelEvent("U0ALICE", "first", "1.1"))
	loopRecord, err := db.Loops().Get(ctx, "loop_terra")
	if err != nil {
		t.Fatal(err)
	}
	if loopRecord.SlackChannelID != "C0FLEET" {
		t.Fatalf("bound to %q, want C0FLEET", loopRecord.SlackChannelID)
	}
	elsewhere, _ := json.Marshal(map[string]any{"team_id": "T0ACME", "event": map[string]any{
		"type": "message", "channel_type": "channel", "channel": "C0RANDOM", "user": "U0ALICE", "text": "second", "ts": "1.2"}})
	adapter.ingest(ctx, heard, elsewhere)
	if msgs, _ := db.Messages().List(ctx, 10); len(msgs) != 1 || msgs[0].Text != "first" {
		t.Fatalf("stored %+v, want the bound channel's message only", msgs)
	}
	if status := heard.status(); status["ignored_events"] != 1 {
		t.Errorf("ignored_events = %v, want 1", status["ignored_events"])
	}
}

// A sender Spool has not seen reaches nobody, and is registered as pending
// under the name Slack gives, for the operator to allow.
func TestUnknownSenderIsRegisteredPending(t *testing.T) {
	adapter, db, got := inboundFixture(t)
	ctx := context.Background()
	adapter.ingest(ctx, &link{loopID: "loop_terra"}, channelEvent("U0BOB", "<@U0terra> hi", "1.1"))
	sender, err := db.SlackSenders().Get(ctx, "U0BOB")
	if err != nil {
		t.Fatal(err)
	}
	if sender.Status != store.SenderPending || sender.Username != "bob" || sender.Display != "Bob" ||
		sender.TeamID != "T0ACME" || sender.PairCode == "" || sender.FirstSeenVia != "group:terra" {
		t.Errorf("registered %+v", sender)
	}
	if msgs, _ := db.Messages().List(ctx, 10); len(msgs) != 0 || got.count("loop_terra") != 0 {
		t.Errorf("a pending sender's message was stored or delivered")
	}
	if loopRecord, _ := db.Loops().Get(ctx, "loop_terra"); loopRecord.SlackChannelID != "" {
		t.Errorf("a pending sender bound the app to %q", loopRecord.SlackChannelID)
	}
}
