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
	// failSends makes the next n sendMessage calls fail the way a blip
	// does — 502, which the bridge treats as worth retrying — and
	// failForever keeps failing until a test says otherwise. A failed call
	// records nothing in sent: the message never existed for the chat.
	failSends   int
	failForever bool
	sendCalls   int
}

// failNextSends makes the stand-in refuse the next n sends. n < 0 refuses
// every send until the test clears it.
func (tg *fakeTelegram) failNextSends(n int) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.failSends, tg.failForever = n, n < 0
}

// sendAttempts counts every sendMessage call the bridge made, refused or not
// — which is how a test sees a retry happen at all.
func (tg *fakeTelegram) sendAttempts() int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return tg.sendCalls
}

type sentMessage struct {
	Token  string
	ChatID int64
	Text   string
	// MessageID is the id this bot's numbering gave the send; ReplyTo is
	// the id it asked Telegram to thread under (0 = a plain post).
	MessageID int64
	ReplyTo   int64
}

// fakePost is one message as the chat holds it: the ids differ per bot,
// because Telegram numbers message_id per bot conversation. A reply can only
// name the id belonging to the bot that receives it.
type fakePost struct {
	ids   map[string]int64 // token → that bot's id for this message
	from  user
	isBot bool
	text  string
	date  int64
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
			ChatID          int64  `json:"chat_id"`
			Text            string `json:"text"`
			ReplyParameters *struct {
				MessageID int64 `json:"message_id"`
			} `json:"reply_parameters"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		tg.mu.Lock()
		tg.sendCalls++
		if tg.failForever || tg.failSends > 0 {
			if !tg.failForever {
				tg.failSends--
			}
			tg.mu.Unlock()
			http.Error(w, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`, 502)
			return
		}
		tg.nextID[token]++
		id := tg.nextID[token]
		var replyTo int64
		if req.ReplyParameters != nil {
			replyTo = req.ReplyParameters.MessageID
		}
		tg.sent = append(tg.sent, sentMessage{Token: token, ChatID: req.ChatID,
			Text: req.Text, MessageID: id, ReplyTo: replyTo})
		tg.mu.Unlock()
		writeOK(w, map[string]any{"message_id": id})
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
func (tg *fakeTelegram) post(chatID int64, chatType, text string, from user) *fakePost {
	return tg.postReply(chatID, chatType, text, from, nil)
}

// postReply is post with a native reply attached. Every bot receives the
// embedded target under its own id for it, and a bot that never saw the
// target — another bot's post — receives the embedded copy with no usable
// id, which is exactly the case the text has to identify.
func (tg *fakeTelegram) postReply(chatID int64, chatType, text string, from user, target *fakePost) *fakePost {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	post := &fakePost{ids: map[string]int64{}, from: from, text: text, date: time.Now().Unix()}
	for _, token := range tg.bots {
		tg.nextID[token]++
		tg.updates++
		post.ids[token] = tg.nextID[token]
		msg := map[string]any{
			"message_id": tg.nextID[token],
			"date":       post.date,
			"text":       text,
			"from": map[string]any{
				"id": from.ID, "is_bot": false,
				"first_name": from.First, "username": from.Username,
			},
			"chat": map[string]any{"id": chatID, "type": chatType},
		}
		if target != nil {
			msg["reply_to_message"] = target.embed(token, chatID, chatType)
		}
		tg.queued[token] = append(tg.queued[token], map[string]any{
			"update_id": tg.updates, "message": msg,
		})
	}
	return post
}

// embed renders the target as Telegram embeds it in a reply, from one bot's
// point of view.
func (p *fakePost) embed(token string, chatID int64, chatType string) map[string]any {
	sender := map[string]any{
		"id": p.from.ID, "is_bot": p.isBot,
		"first_name": p.from.First, "username": p.from.Username,
	}
	return map[string]any{
		"message_id": p.ids[token], // 0 when this bot never saw it
		"date":       p.date,
		"text":       p.text,
		"from":       sender,
		"chat":       map[string]any{"id": chatID, "type": chatType},
	}
}

// sentPost turns a bot's own send into a post other messages can reply to:
// only the bot that sent it holds an id for it.
func (tg *fakeTelegram) sentPost(t *testing.T, chatID int64, text string) *fakePost {
	t.Helper()
	return tg.sentPostFrom(t, chatID, "", text)
}

// sentPostFrom is sentPost narrowed to one bot's send, for when several bots
// posted the same words.
func (tg *fakeTelegram) sentPostFrom(t *testing.T, chatID int64, token, text string) *fakePost {
	t.Helper()
	sent := tg.waitSentFrom(t, chatID, token, text)
	return &fakePost{
		ids:   map[string]int64{sent.Token: sent.MessageID},
		from:  user{ID: 9000, First: botUsername(sent.Token)},
		isBot: true,
		text:  sent.Text,
		date:  time.Now().Unix(),
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

// sentTo returns every message any bot has posted to chatID so far.
func (tg *fakeTelegram) sentTo(chatID int64) []sentMessage {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	var out []sentMessage
	for _, m := range tg.sent {
		if m.ChatID == chatID {
			out = append(out, m)
		}
	}
	return out
}

// waitSent blocks until a bot posts a message containing text to chatID.
func (tg *fakeTelegram) waitSent(t *testing.T, chatID int64, text string) sentMessage {
	t.Helper()
	return tg.waitSentFrom(t, chatID, "", text)
}

// waitSentFrom is waitSent restricted to one bot's token ("" = any bot).
func (tg *fakeTelegram) waitSentFrom(t *testing.T, chatID int64, token, text string) sentMessage {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range tg.sentTo(chatID) {
			if (token == "" || m.Token == token) && strings.Contains(m.Text, text) {
				return m
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("bot %q never sent %q to chat %d", token, text, chatID)
	return sentMessage{}
}

type activityMessage struct {
	ID                 int64    `json:"id"`
	Origin             string   `json:"origin"`
	Author             string   `json:"author"`
	Text               string   `json:"text"`
	DeliveredTo        []string `json:"delivered_to"`
	Conversation       string   `json:"conversation"`
	ConversationLoopID string   `json:"conversation_loop_id"`
	ReplyToID          int64    `json:"reply_to_id"`
	SendFailedAt       int64    `json:"send_failed_at"`
	SendError          string   `json:"send_error"`
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
// Optional overrides merge into alpha's, then beta's, create request.
func startTelegramFleet(t *testing.T, operator user, overrides ...map[string]any) (*server, *fakeTelegram) {
	t.Helper()
	tg := startFakeTelegram(t, "alpha", "beta")
	srv := startTelegramServer(t, t.TempDir(), tg)
	for i, name := range []string{"alpha", "beta"} {
		req := map[string]any{"tg_bot_token": name}
		if i < len(overrides) {
			for k, v := range overrides[i] {
				req[k] = v
			}
		}
		srv.createLoop(name, req)
	}

	// The first group message only registers the operator as pending — an
	// unknown sender is turned away before the bind. The second one binds
	// every bot to the group; neither reaches a loop.
	tg.post(groupChatID, "supergroup", "hello", operator)
	srv.allowSender(operator.ID)
	tg.post(groupChatID, "supergroup", "binding", operator)
	settleBindings()
	return srv, tg
}

// startTelegramServer spawns a server pointed at the stand-in Telegram API.
// Every test that binds bots goes through here rather than calling
// startServerArgs itself, so the shortened bind margin and settleBindings
// cannot drift apart: the margin exists to cover Telegram's clock skew, the
// stand-in has none, and 24 rows sleeping out the production five seconds
// was a third of the suite's runtime (#136).
func startTelegramServer(t *testing.T, dataDir string, tg *fakeTelegram) *server {
	t.Helper()
	return startServerArgs(t, dataDir, "--runtime", "bare",
		"--telegram-api-base", tg.srv.URL,
		"--telegram-bind-settle-sec", "1")
}

// settleBindings waits out the margin a freshly bound bot serves before it
// can win an ingest election (the bridge's settle margin, ADR-0020).
// The fleet harness sets that margin to a second; the wait is longer than
// the margin because the comparison is against Telegram's message dates,
// which are whole seconds, and is strict — a message has to be dated at
// least one whole second past the margin to qualify.
func settleBindings() { time.Sleep(2500 * time.Millisecond) }

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

// A loop's owner_dm send must reach the captured DM chat as the loop's own
// bot, never surface in the group, and deliver nothing to a peer the private
// text names (ADR-0025 scenarios, #37).
func TestOwnerDMSendReachesTheDMChat(t *testing.T) {
	operator := user{ID: 4747, First: "Operator", Username: "operator"}
	// line 1 answers the creation tick; line 2 answers the owner's DM
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"owner_dm","text":"@beta hello owner"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": wsAlpha})

	tg.dm("alpha", operator, "hi alpha")

	sent := tg.waitSent(t, operator.ID, "hello owner")
	if sent.Token != "alpha" {
		t.Fatalf("owner DM delivered by bot %q, want alpha's own", sent.Token)
	}
	// let any misrouted delivery drain before the negative checks
	time.Sleep(2 * time.Second)
	for _, m := range tg.sentTo(groupChatID) {
		if strings.Contains(m.Text, "hello owner") {
			t.Fatalf("owner_dm send surfaced in the group: %q", m.Text)
		}
	}
	for _, tn := range srv.completed("beta") {
		if tn.Trigger == "message" {
			t.Fatalf("private text naming beta delivered to it: %s", dump(tn))
		}
	}
}

// A web message to one loop is its private control_room thread: it must not
// be mirrored to the group, while a composer post with the group
// destination still is.
func TestControlRoomStaysOutOfTheGroup(t *testing.T) {
	operator := user{ID: 4848, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	srv.message("alpha", "private control room note")
	srv.mustJSON("POST", "/api/loops/alpha/message",
		map[string]any{"author": "operator", "text": "public group post", "destination": "group"}, nil)

	tg.waitSent(t, groupChatID, "public group post")
	// the mirror consumes bus items in order and alpha's bot queues sends
	// FIFO: had the earlier private note leaked, it would already be posted
	for _, m := range tg.sentTo(groupChatID) {
		if strings.Contains(m.Text, "private control room note") {
			t.Fatalf("control_room message surfaced in the group: %q", m.Text)
		}
	}
}

// Unaddressed human group chatter is stored and visible but wakes no loop;
// a mention delivers to the mentioned loop alone (ADR-0025 selective wake).
func TestUnaddressedGroupChatterWakesNoLoop(t *testing.T) {
	operator := user{ID: 4949, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	const chatter = "just us humans talking"
	tg.post(groupChatID, "supergroup", chatter, operator)
	srv.waitForMessage(chatter)
	if d := srv.activityWith(chatter)[0].DeliveredTo; len(d) != 0 {
		t.Fatalf("unaddressed chatter delivered to %v, want nobody", d)
	}

	// the mention that follows proves delivery works while the chatter,
	// ingested first, still reached nobody
	tg.post(groupChatID, "supergroup", "@alpha only you", operator)
	srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "only you")
	})
	for _, name := range []string{"alpha", "beta"} {
		for _, tn := range srv.completed(name) {
			if strings.Contains(tn.ResultText, chatter) {
				t.Fatalf("unaddressed chatter reached %s: %s", name, dump(tn))
			}
		}
	}
	for _, tn := range srv.completed("beta") {
		if tn.Trigger == "message" {
			t.Fatalf("a message addressed to alpha alone woke beta: %s", dump(tn))
		}
	}
}

// The per-loop composer declares its destination (ADR-0026): group posts to
// the shared conversation — delivered to the loop and mirrored to the bound
// telegram group — and a destination outside the picker's two is refused.
func TestComposerGroupDestination(t *testing.T) {
	operator := user{ID: 5050, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	srv.mustJSON("POST", "/api/loops/alpha/message",
		map[string]any{"author": "operator", "text": "to the group", "destination": "group"}, nil)
	tg.waitSent(t, groupChatID, "to the group")
	srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "to the group")
	})
	stored := srv.activityWith("to the group")
	if len(stored) != 1 || stored[0].Conversation != "group" {
		t.Fatalf("group-destination message stored as: %s", dump(stored))
	}

	if resp, _ := srv.do("POST", "/api/loops/alpha/message",
		map[string]any{"text": "x", "destination": "owner_dm"}); resp.StatusCode != 400 {
		t.Fatalf("owner_dm composer destination accepted: %d", resp.StatusCode)
	}
}

// A private inbound message naming a peer delivers only to its own loop:
// mentions in owner_dm or control_room text never add recipients, the named
// peer gets no input or wake, and nothing surfaces in the group (ADR-0025).
func TestPrivateInboundNeverFansOut(t *testing.T) {
	operator := user{ID: 5454, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	const dmText = "please check @beta quietly"
	const webText = "control room note about @beta"
	tg.dm("alpha", operator, dmText)
	srv.mustJSON("POST", "/api/loops/alpha/message", map[string]any{"text": webText}, nil)

	for _, text := range []string{dmText, webText} {
		srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
			return strings.Contains(tn.ResultText, text)
		})
		stored := srv.activityWith(text)
		if len(stored) != 1 || len(stored[0].DeliveredTo) != 1 {
			t.Fatalf("%q delivered to %v, want alpha alone", text, dump(stored))
		}
	}
	// let any misrouted delivery drain before the negative checks
	time.Sleep(2 * time.Second)
	for _, tn := range srv.completed("beta") {
		if tn.Trigger == "message" {
			t.Fatalf("private text naming beta delivered to it: %s", dump(tn))
		}
	}
	for _, m := range tg.sentTo(groupChatID) {
		if strings.Contains(m.Text, dmText) || strings.Contains(m.Text, webText) {
			t.Fatalf("private inbound surfaced in the group: %q", m.Text)
		}
	}
}

// A DM and a group message arriving together must produce separate replies
// in separate turns and separate destinations — the DM's answer reaches the
// DM chat, the group's reaches the group, and neither crosses over
// (ADR-0025 scenarios).
func TestMixedDMAndGroupArrivalsAnswerSeparately(t *testing.T) {
	operator := user{ID: 5151, First: "Operator", Username: "operator"}
	// line 1 answers the creation tick; line 2 hangs so both arrivals
	// queue; lines 3 and 4 answer each queued conversation with a real
	// send to its own destination
	ws := workspaceWithScript(t, "!ctx 0\n!hang 4\n"+
		`!send {"destination":"owner_dm","text":"private answer"} handled dm hello`+"\n"+
		`!send {"destination":"group","text":"@operator group answer"} handled group hello`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.dm("alpha", operator, "start hanging")
	srv.waitState("alpha", "busy", 10*time.Second) // alpha is inside the hang
	tg.dm("alpha", operator, "dm hello")
	srv.waitForMessage("dm hello") // ingested first: the queue order is fixed
	tg.post(groupChatID, "supergroup", "@alpha group hello", operator)

	dmTurn := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "handled dm hello")
	})
	groupTurn := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "handled group hello")
	})
	if dmTurn.ID == groupTurn.ID {
		t.Fatalf("DM and group inputs shared one turn: %s", dump(dmTurn))
	}
	// the recorded envelopes are the inputs each turn actually received —
	// each must carry its own conversation's message and not the other's
	inputs := srv.turnInputs("alpha")
	for _, c := range []struct {
		tn           turn
		want, forbid string
	}{
		{dmTurn, "dm hello", "group hello"},
		{groupTurn, "group hello", "dm hello"},
	} {
		joined := strings.Join(inputs[c.tn.ID], "\n")
		if !strings.Contains(joined, c.want) || strings.Contains(joined, c.forbid) {
			t.Fatalf("turn %s inputs = %q, want %q and never %q", c.tn.ID, joined, c.want, c.forbid)
		}
	}

	if sent := tg.waitSent(t, operator.ID, "private answer"); sent.Token != "alpha" {
		t.Fatalf("DM answer delivered by bot %q, want alpha's own", sent.Token)
	}
	tg.waitSent(t, groupChatID, "group answer")
	for _, m := range tg.sentTo(groupChatID) {
		if strings.Contains(m.Text, "private answer") {
			t.Fatalf("the DM conversation's answer surfaced in the group: %q", m.Text)
		}
	}
	for _, m := range tg.sentTo(operator.ID) {
		if strings.Contains(m.Text, "group answer") {
			t.Fatalf("the group conversation's answer arrived as a DM: %q", m.Text)
		}
	}
}

// Only the loop's owner holds a private conversation with it (#73): a
// non-owner's DM is turned away with a readable reason and never becomes a
// turn, and the owner's own DM is answered in the owner's chat while a
// stranger's arrives alongside it. Isolating *two* humans' private
// conversations returns with the non-owner DM design.
func TestOnlyTheOwnerHoldsAPrivateConversation(t *testing.T) {
	operator := user{ID: 5252, First: "Operator", Username: "operator"}
	friend := user{ID: 5353, First: "Friend", Username: "friend"}
	ws := workspaceWithScript(t, "!ctx 0\n!hang 4\n"+
		`!send {"destination":"owner_dm","text":"answer for op"} handled from op`+"\n"+
		`!send {"destination":"owner_dm","text":"proactive note"} handled wake`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	// the friend's first DM only registers them as pending; allowlisting
	// them lets them reach a bot, but not this loop's private conversation
	tg.dm("alpha", friend, "knock")
	srv.allowSender(friend.ID)

	tg.dm("alpha", operator, "start hanging")
	srv.waitState("alpha", "busy", 10*time.Second) // alpha is inside the hang
	tg.dm("alpha", operator, "from op")
	srv.waitForMessage("from op")
	tg.dm("alpha", friend, "from friend")

	opTurn := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "handled from op")
	})
	tg.waitSent(t, operator.ID, "answer for op")
	tg.waitSent(t, friend.ID, "direct messages only from")

	// nothing of the friend's reached the loop: not as a turn input, not as
	// a stored message, not as an answer
	inputs := srv.turnInputs("alpha")
	for id, envelopes := range inputs {
		if strings.Contains(strings.Join(envelopes, "\n"), "from friend") {
			t.Fatalf("turn %s was given a non-owner's DM", id)
		}
	}
	if len(srv.activityWith("from friend")) != 0 {
		t.Fatal("a non-owner's DM was stored as a message")
	}
	if !strings.Contains(strings.Join(inputs[opTurn.ID], "\n"), "from op") {
		t.Fatalf("the owner's DM never reached its turn: %q", inputs[opTurn.ID])
	}
	for _, m := range tg.sentTo(friend.ID) {
		if strings.Contains(m.Text, "answer for op") {
			t.Fatalf("the owner's answer reached a non-owner: %q", m.Text)
		}
	}
	for _, m := range tg.sentTo(groupChatID) {
		if strings.Contains(m.Text, "answer for") {
			t.Fatalf("a private answer surfaced in the group: %q", m.Text)
		}
	}

	// A turn with no DM of its own still reaches the owner: the address is
	// the configured owner's, not the latest chat to have written
	// (ADR-0026, amended for #73).
	srv.mustJSON("POST", "/api/loops/alpha/wake", nil, nil)
	tg.waitSent(t, operator.ID, "proactive note")
	for _, m := range tg.sentTo(friend.ID) {
		if strings.Contains(m.Text, "proactive note") {
			t.Fatalf("a proactive owner DM went to a non-owner: %q", m.Text)
		}
	}
}
