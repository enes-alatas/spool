//go:build integration

package itest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTelegram stands in for the Bot API. It models the one property that
// makes dedup hard: a message posted to a chat is handed to every bot that
// can see it, each under that bot's own message_id counter.
type fakeTelegram struct {
	srv *httptest.Server

	mu      sync.Mutex
	bots    []string                    // tokens, in registration order
	queued  map[string][]map[string]any // token → pending updates
	nextID  map[string]int64            // token → next message_id
	updates int64
	sent    []sentMessage
}

type sentMessage struct {
	Token  string
	ChatID int64
	Text   string
}

func startFakeTelegram(t *testing.T, tokens ...string) *fakeTelegram {
	t.Helper()
	tg := &fakeTelegram{
		queued: map[string][]map[string]any{},
		nextID: map[string]int64{},
	}
	for _, token := range tokens {
		tg.addBot(token)
	}
	tg.srv = httptest.NewServer(http.HandlerFunc(tg.handle))
	t.Cleanup(tg.srv.Close)
	return tg
}

// addBot registers a bot in the chat. Each one numbers messages from its own
// history, so the counters are unrelated: the same human message reaches one
// bot as 101 and another as 501. Starting them level would hide the bug
// under test.
func (tg *fakeTelegram) addBot(token string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.nextID[token] = int64(100 + len(tg.bots)*400)
	tg.bots = append(tg.bots, token)
}

func (tg *fakeTelegram) handle(w http.ResponseWriter, r *http.Request) {
	// /bot<token>/<method>
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/bot"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	token, method := parts[0], parts[1]
	switch method {
	case "getMe":
		writeOK(w, map[string]any{"id": 1, "is_bot": true, "username": botUsername(token)})
	case "getUpdates":
		writeOK(w, tg.drain(token))
	case "sendMessage":
		var req struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		tg.mu.Lock()
		tg.sent = append(tg.sent, sentMessage{Token: token, ChatID: req.ChatID, Text: req.Text})
		tg.mu.Unlock()
		writeOK(w, map[string]any{"message_id": 1})
	default:
		writeOK(w, map[string]any{})
	}
}

func botUsername(token string) string { return token + "_bot" }

func writeOK(w http.ResponseWriter, result any) {
	body, _ := json.Marshal(result)
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, body)
}

// drain returns a bot's pending updates, waiting briefly so pollers don't
// spin. The real API long-polls for up to 50s.
func (tg *fakeTelegram) drain(token string) []map[string]any {
	deadline := time.Now().Add(time.Second)
	for {
		tg.mu.Lock()
		pending := tg.queued[token]
		tg.queued[token] = nil
		tg.mu.Unlock()
		if len(pending) > 0 || time.Now().After(deadline) {
			return pending
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// post delivers one human message to every bot listening on the chat, each
// with its own message_id — exactly what Telegram does.
func (tg *fakeTelegram) post(chatID int64, chatType, text string, from user) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	for _, token := range tg.bots {
		tg.nextID[token]++
		tg.updates++
		tg.queued[token] = append(tg.queued[token], map[string]any{
			"update_id": tg.updates,
			"message": map[string]any{
				"message_id": tg.nextID[token],
				"date":       time.Now().Unix(),
				"text":       text,
				"from": map[string]any{
					"id": from.ID, "is_bot": false,
					"first_name": from.First, "username": from.Username,
				},
				"chat": map[string]any{"id": chatID, "type": chatType},
			},
		})
	}
}

// dm delivers a private message to a single bot. Telegram uses the human's
// user id as the chat id in every one of their private chats, and numbers
// message_id per bot — so two bots' DMs collide on both fields.
func (tg *fakeTelegram) dm(token string, from user, text string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.nextID[token]++
	tg.updates++
	tg.queued[token] = append(tg.queued[token], map[string]any{
		"update_id": tg.updates,
		"message": map[string]any{
			"message_id": tg.nextID[token],
			"date":       time.Now().Unix(),
			"text":       text,
			"from": map[string]any{
				"id": from.ID, "is_bot": false,
				"first_name": from.First, "username": from.Username,
			},
			"chat": map[string]any{"id": from.ID, "type": "private"},
		},
	})
}

type user struct {
	ID       int64
	First    string
	Username string
}

type activityMessage struct {
	ID                 int64    `json:"id"`
	Origin             string   `json:"origin"`
	Author             string   `json:"author"`
	Text               string   `json:"text"`
	DeliveredTo        []string `json:"delivered_to"`
	Conversation       string   `json:"conversation"`
	ConversationLoopID string   `json:"conversation_loop_id"`
}

func (s *server) activity() []activityMessage {
	s.t.Helper()
	var msgs []activityMessage
	s.mustJSON("GET", "/api/activity?limit=200", nil, &msgs)
	return msgs
}

func (s *server) activityWith(text string) []activityMessage {
	s.t.Helper()
	var out []activityMessage
	for _, m := range s.activity() {
		if m.Text == text {
			out = append(out, m)
		}
	}
	return out
}

// waitForMessage blocks until text has been ingested at least once.
func (s *server) waitForMessage(text string) {
	s.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.activityWith(text)) > 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.t.Fatalf("message %q never ingested", text)
}

type tgSender struct {
	TGUserID int64  `json:"tg_user_id"`
	Status   string `json:"status"`
}

// allowSender waits for the pending record the first message creates, then
// approves it: loops only hear allowlisted humans.
func (s *server) allowSender(id int64) {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var senders []tgSender
		s.mustJSON("GET", "/api/telegram/senders", nil, &senders)
		for _, sender := range senders {
			if sender.TGUserID == id {
				s.mustJSON("POST", fmt.Sprintf("/api/telegram/senders/%d/allow", id), nil, nil)
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.t.Fatalf("sender %d never registered", id)
}

// startTelegramFleet brings up two bot-backed loops against a fake Bot API,
// with the operator already allowlisted and both bots bound to the group.
func startTelegramFleet(t *testing.T, operator user) (*server, *fakeTelegram) {
	t.Helper()
	tg := startFakeTelegram(t, "alpha", "beta")
	srv := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--telegram-api-base", tg.srv.URL)
	srv.createLoop("alpha", map[string]any{"tg_bot_token": "alpha"})
	srv.createLoop("beta", map[string]any{"tg_bot_token": "beta"})

	// The first group message only registers the operator as pending — an
	// unknown sender is turned away before the bind. The second one binds
	// every bot to the group; neither reaches a loop.
	tg.post(groupChatID, "supergroup", "hello", operator)
	srv.allowSender(operator.ID)
	tg.post(groupChatID, "supergroup", "binding", operator)
	settleBindings()
	return srv, tg
}

// settleBindings waits out the margin a freshly bound bot serves before it
// can win an ingest election (bindSettle in internal/telegram, ADR-0020).
func settleBindings() { time.Sleep(7 * time.Second) }

const groupChatID int64 = -1001234567890

// One human message in the group must be stored once, however many bots saw
// it, and still reach every loop it mentions: ingest is one bot's job,
// delivery is the router's.
func TestGroupMessageIngestedOnceAndDeliveredToAllMentions(t *testing.T) {
	operator := user{ID: 4242, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	const text = "@alpha @beta status please"
	tg.post(groupChatID, "supergroup", text, operator)
	srv.waitForMessage(text)

	// give any second ingester time to add its own row
	time.Sleep(2 * time.Second)
	stored := srv.activityWith(text)

	if len(stored) != 1 {
		t.Fatalf("group message stored %d times, want 1", len(stored))
	}
	if len(stored[0].DeliveredTo) != 2 {
		t.Fatalf("delivered_to = %v, want both mentioned loops", stored[0].DeliveredTo)
	}
}

// A bot joining a live group must not ingest the message that binds it: the
// incumbents are already handling that message, and a set read as "whoever
// is bound right now" changes underneath them mid-message.
func TestBotJoiningLiveGroupDoesNotDoubleIngest(t *testing.T) {
	operator := user{ID: 4444, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	srv.createLoop("gamma", map[string]any{"tg_bot_token": "gamma"})
	tg.addBot("gamma")

	const joining = "@alpha @beta while gamma joins"
	tg.post(groupChatID, "supergroup", joining, operator)
	srv.waitForMessage(joining)

	const settled = "@alpha @beta once gamma settled"
	settleBindings()
	tg.post(groupChatID, "supergroup", settled, operator)
	srv.waitForMessage(settled)

	// let any second ingester add its own row before counting
	time.Sleep(2 * time.Second)
	for _, text := range []string{joining, settled} {
		if stored := srv.activityWith(text); len(stored) != 1 {
			t.Fatalf("%q stored %d times, want 1", text, len(stored))
		}
	}
}

// Distinct DMs to distinct bots share a chat id (the human's user id) and
// reuse message_id per bot, so they used to collide on the dedup key and the
// second one vanished. Both must land, each for its own loop.
func TestDirectMessagesToDifferentBotsBothLand(t *testing.T) {
	operator := user{ID: 4343, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	tg.dm("alpha", operator, "for alpha")
	tg.dm("beta", operator, "for beta")

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if len(srv.activityWith("for alpha")) == 1 && len(srv.activityWith("for beta")) == 1 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("DMs not both stored: alpha=%d beta=%d",
		len(srv.activityWith("for alpha")), len(srv.activityWith("for beta")))
}
