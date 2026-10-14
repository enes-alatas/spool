//go:build integration

package itest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
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
	// failText refuses, the same way, every send whose text contains it:
	// how a test fails one part of a long message and not the others.
	failText  string
	sendCalls int
	// stallSends holds the next n sendMessage calls without an answer
	// until the bridge hangs up on them: a connection that died after the
	// request was written, as the hub sees one. A held call records nothing
	// in sent.
	stallSends int
	// lateSends posts the next n sendMessage calls and then holds each
	// answer until the bridge hangs up: a Telegram slow to answer a send it
	// has made, which the hub cannot tell from a dead connection (#800).
	lateSends int
	// holdGetMe stands in for a slow api.telegram.org: getMe blocks on it
	// until the test lets go. Real getMe calls take a round-trip, and the
	// hub holds a copy of the loop row across one (#164) — a window a test
	// cannot hit reliably by timing alone.
	holdGetMe  chan struct{}
	getMeEntry chan struct{}
	// refused are the tokens getMe answers 401 Unauthorized, as Telegram
	// does a token it does not know.
	refused map[string]bool
	// files are the bytes behind each file_id a media message names, and
	// downloads counts every fetch of one — how a test sees that a file
	// several bots saw was downloaded once (#123).
	files     map[string][]byte
	downloads int
	// titles are the group titles Telegram reports, by chat; a chat with
	// none is reported untitled.
	titles map[int64]string
	// reactionsSet are the setMessageReaction calls the bridge made.
	reactionsSet []setReaction
	// pollsSent and pollsStopped are the sendPoll and stopPoll calls the
	// bridge made.
	pollsSent    []sentPoll
	pollsStopped []setReaction
}

// setReaction is one setMessageReaction call: a bot's reaction on a
// message, by that bot's id for it ("" = cleared).
type setReaction struct {
	Token     string
	ChatID    int64
	MessageID int64
	Emoji     string
}

// refuse makes getMe answer 401 for token from here on.
func (tg *fakeTelegram) refuse(token string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if tg.refused == nil {
		tg.refused = map[string]bool{}
	}
	tg.refused[token] = true
}

// blockGetMe makes every getMe from here on hang until the returned release
// func is called, and returns a channel that reports each call entering the
// block. That is the hub's token round-trip held open, so a test can land a
// concurrent write inside it rather than racing for it.
func (tg *fakeTelegram) blockGetMe() (entered <-chan struct{}, release func()) {
	tg.mu.Lock()
	hold := make(chan struct{})
	entry := make(chan struct{}, 8)
	tg.holdGetMe, tg.getMeEntry = hold, entry
	tg.mu.Unlock()
	var once sync.Once
	return entry, func() {
		once.Do(func() {
			tg.mu.Lock()
			tg.holdGetMe, tg.getMeEntry = nil, nil
			tg.mu.Unlock()
			close(hold)
		})
	}
}

// failNextSends makes the stand-in refuse the next n sends. n < 0 refuses
// every send until the test clears it.
func (tg *fakeTelegram) failNextSends(n int) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.failSends, tg.failForever = n, n < 0
}

// lateNextSends makes the stand-in post the next n sends and leave them
// unanswered.
func (tg *fakeTelegram) lateNextSends(n int) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.lateSends = n
}

// stallNextSends makes the stand-in leave the next n sends unanswered.
func (tg *fakeTelegram) stallNextSends(n int) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.stallSends = n
}

// failSendsContaining makes the stand-in refuse every send whose text
// contains text, until the test clears it with "".
func (tg *fakeTelegram) failSendsContaining(text string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.failText = text
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
	// Method is the Bot API call that sent it. A sendPhoto or sendDocument
	// carries its caption as Text, and the file it uploaded (#123).
	Method   string
	FileName string
	File     []byte
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
		files:  map[string][]byte{},
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
	if rest, ok := strings.CutPrefix(r.URL.Path, "/file/bot"); ok {
		// /file/bot<token>/<file_path>, where getFile's file_path is the id
		_, fileID, _ := strings.Cut(rest, "/")
		tg.mu.Lock()
		body, found := tg.files[fileID]
		if found {
			tg.downloads++
		}
		tg.mu.Unlock()
		if !found {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
		return
	}
	// /bot<token>/<method>
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/bot"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	token, method := parts[0], parts[1]
	switch method {
	case "getMe":
		tg.mu.Lock()
		refused, hold, entry := tg.refused[token], tg.holdGetMe, tg.getMeEntry
		tg.mu.Unlock()
		if refused {
			http.Error(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, 401)
			return
		}
		if hold != nil {
			select {
			case entry <- struct{}{}:
			default:
			}
			<-hold
		}
		writeOK(w, map[string]any{"id": 1, "is_bot": true, "username": botUsername(token)})
	case "getUpdates":
		writeOK(w, tg.drain(token))
	case "getFile":
		var req struct {
			FileID string `json:"file_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		tg.mu.Lock()
		body, found := tg.files[req.FileID]
		tg.mu.Unlock()
		if !found {
			http.Error(w, `{"ok":false,"error_code":400,"description":"Bad Request: invalid file_id"}`, 400)
			return
		}
		writeOK(w, map[string]any{"file_id": req.FileID, "file_size": len(body), "file_path": req.FileID})
	case "sendMessage":
		var req struct {
			ChatID          int64  `json:"chat_id"`
			Text            string `json:"text"`
			ReplyParameters *struct {
				MessageID int64 `json:"message_id"`
			} `json:"reply_parameters"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		// read whole, so the server watches the connection and sees the
		// bridge hang up on a held call
		_, _ = io.Copy(io.Discard, r.Body)
		tg.mu.Lock()
		tg.sendCalls++
		if tg.stallSends > 0 {
			tg.stallSends--
			tg.mu.Unlock()
			<-r.Context().Done()
			return
		}
		if tg.failForever || tg.failSends > 0 || (tg.failText != "" && strings.Contains(req.Text, tg.failText)) {
			if !tg.failForever && tg.failSends > 0 {
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
		if tg.lateSends > 0 {
			tg.lateSends--
			tg.mu.Unlock()
			<-r.Context().Done()
			return
		}
		tg.mu.Unlock()
		writeOK(w, map[string]any{"message_id": id})
	case "sendPhoto", "sendDocument":
		field := map[string]string{"sendPhoto": "photo", "sendDocument": "document"}[method]
		file, header, err := r.FormFile(field)
		if err != nil {
			http.Error(w, `{"ok":false,"error_code":400,"description":"Bad Request: no file"}`, 400)
			return
		}
		body, _ := io.ReadAll(file)
		chatID, _ := strconv.ParseInt(r.FormValue("chat_id"), 10, 64)
		var reply struct {
			MessageID int64 `json:"message_id"`
		}
		if params := r.FormValue("reply_parameters"); params != "" {
			_ = json.Unmarshal([]byte(params), &reply)
		}
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
		tg.sent = append(tg.sent, sentMessage{Token: token, ChatID: chatID, Text: r.FormValue("caption"),
			MessageID: id, ReplyTo: reply.MessageID, Method: method, FileName: header.Filename, File: body})
		tg.mu.Unlock()
		writeOK(w, map[string]any{"message_id": id})
	case "sendPoll":
		var req struct {
			ChatID   int64  `json:"chat_id"`
			Question string `json:"question"`
			Options  []struct {
				Text string `json:"text"`
			} `json:"options"`
			IsAnonymous *bool `json:"is_anonymous"`
			Multiple    bool  `json:"allows_multiple_answers"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		tg.mu.Lock()
		tg.sendCalls++
		if tg.failForever {
			tg.mu.Unlock()
			http.Error(w, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`, 502)
			return
		}
		tg.nextID[token]++
		poll := sentPoll{Token: token, ChatID: req.ChatID, Question: req.Question, MessageID: tg.nextID[token],
			PollID: fmt.Sprintf("poll-%s-%d", token, tg.nextID[token]), Multiple: req.Multiple,
			Anonymous: req.IsAnonymous == nil || *req.IsAnonymous} // Telegram's default is anonymous
		for _, option := range req.Options {
			poll.Options = append(poll.Options, option.Text)
		}
		tg.pollsSent = append(tg.pollsSent, poll)
		tg.mu.Unlock()
		writeOK(w, map[string]any{"message_id": poll.MessageID, "poll": map[string]any{"id": poll.PollID}})
	case "stopPoll":
		var req struct {
			ChatID    int64 `json:"chat_id"`
			MessageID int64 `json:"message_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		tg.mu.Lock()
		tg.pollsStopped = append(tg.pollsStopped, setReaction{Token: token, ChatID: req.ChatID, MessageID: req.MessageID})
		tg.mu.Unlock()
		writeOK(w, map[string]any{"is_closed": true})
	case "setMessageReaction":
		var req struct {
			ChatID    int64 `json:"chat_id"`
			MessageID int64 `json:"message_id"`
			Reaction  []struct {
				Emoji string `json:"emoji"`
			} `json:"reaction"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		set := setReaction{Token: token, ChatID: req.ChatID, MessageID: req.MessageID}
		if len(req.Reaction) > 0 {
			set.Emoji = req.Reaction[0].Emoji
		}
		tg.mu.Lock()
		tg.reactionsSet = append(tg.reactionsSet, set)
		tg.mu.Unlock()
		writeOK(w, true)
	default:
		writeOK(w, map[string]any{})
	}
}

// botUsername is what the stand-in's getMe answers for a token. It embeds
// the token, which a real bot's username never does — so every token here
// stays a short label, below redact.MinLength. A fixture token long enough
// to be redacted would come back out of the API as "<redacted:tg_bot_token>_bot"
// and read as a product bug rather than the fixture artefact it is (#150).
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

// postReply is post with a native reply attached. In a supergroup every bot
// receives the embedded target under its own id for it, and a bot that never
// saw the target — another bot's post — receives the embedded copy with no
// usable id, which is the case the text has to identify.
//
// A basic group ("group") is different, and the fleet's own group is one. Each
// member there has its own copy of every message, and a bot never receives
// another bot's post, so it has no copy to embed: only a bot holding the
// target finds reply_to_message in the update at all. The fleet's store
// showed it on #424. The other bots' ids for the reply run on from their
// last message with no gap where the post would be.
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
			"chat": map[string]any{"id": chatID, "type": chatType, "title": tg.titles[chatID]},
		}
		if target != nil && (chatType != "group" || target.ids[token] != 0) {
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
		from:  user{ID: 9000, First: botUsername(sent.Token), Username: botUsername(sent.Token)},
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

// media is a photo or a document to attach to a posted message, as
// Telegram describes it. The bytes are served under the file id; size is
// what the message declares, which a test can set larger than the bytes.
type media struct {
	photo  bool
	name   string
	bytes  []byte
	size   int64
	width  int
	height int
}

// withMedia is post and dm for a message carrying a file, the caption as
// its words. It serves the file's bytes, and puts the same file_id in every
// bot's copy — Telegram's file ids differ per bot, but a test that counts
// downloads wants one file.
func (tg *fakeTelegram) withMedia(tokens []string, chatID int64, chatType, caption string, from user, file media) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	fileID := fmt.Sprintf("file-%d", len(tg.files)+1)
	tg.files[fileID] = file.bytes
	size := file.size
	if size == 0 {
		size = int64(len(file.bytes))
	}
	date := time.Now().Unix()
	for _, token := range tokens {
		tg.nextID[token]++
		tg.updates++
		msg := map[string]any{
			"message_id": tg.nextID[token],
			"date":       date,
			"caption":    caption,
			"from": map[string]any{
				"id": from.ID, "is_bot": false,
				"first_name": from.First, "username": from.Username,
			},
			"chat": map[string]any{"id": chatID, "type": chatType},
		}
		if file.photo {
			// a thumbnail first, then the full size: the bridge takes the last
			msg["photo"] = []map[string]any{
				{"file_id": "thumb-" + fileID, "width": 8, "height": 4, "file_size": 64},
				{"file_id": fileID, "width": file.width, "height": file.height, "file_size": size},
			}
		} else {
			msg["document"] = map[string]any{"file_id": fileID, "file_name": file.name, "file_size": size}
		}
		tg.queued[token] = append(tg.queued[token], map[string]any{"update_id": tg.updates, "message": msg})
	}
}

// downloadCount is how many file downloads the bots have made.
func (tg *fakeTelegram) downloadCount() int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return tg.downloads
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

// sentAnywhere reports the first message any bot posted, to any chat, that
// contains text — the check for words that must not have left the hub at
// all, where asking about one chat would miss a leak into another.
func (tg *fakeTelegram) sentAnywhere(text string) (sentMessage, bool) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	for _, m := range tg.sent {
		if strings.Contains(m.Text, text) {
			return m, true
		}
	}
	return sentMessage{}, false
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
	Channel            string   `json:"channel"`
	ReplyToID          int64    `json:"reply_to_id"`
	SendFailedAt       int64    `json:"send_failed_at"`
	SendError          string   `json:"send_error"`
	SendResolvedAt     int64    `json:"send_resolved_at"`
	SendResolution     string   `json:"send_resolution"`
	SendResentAs       int64    `json:"send_resent_as"`
	Mirror             string   `json:"mirror"`
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

// allowSender makes person an allowed sender the one way there is
// (ADR-0051): a hub user of their own, named for them, asks for a Telegram
// link code, and person sends it to bot in a DM and confirms it. It
// answers with that user's session. The user is a member, so the link
// makes person the owner of nothing.
func (s *server) allowSender(tg *fakeTelegram, bot string, person user) string {
	s.t.Helper()
	return s.allowSenderAs(tg, bot, person, "member")
}

// allowOwner is allowSender for the operator: their user is an owner, so
// the link also makes person the owner of every loop with none (ADR-0051).
func (s *server) allowOwner(tg *fakeTelegram, bot string, person user) string {
	s.t.Helper()
	return s.allowSenderAs(tg, bot, person, "owner")
}

func (s *server) allowSenderAs(tg *fakeTelegram, bot string, person user, role string) string {
	s.t.Helper()
	name := fmt.Sprintf("tg%d", person.ID)
	session := s.signedInAs(name, role)
	s.confirmLink(tg, bot, person, session, name)
	return session
}

// confirmLink sends person's code from session to bot in a DM, and their
// YES, and waits for the link to name.
func (s *server) confirmLink(tg *fakeTelegram, bot string, person user, session, name string) {
	s.t.Helper()
	tg.dm(bot, person, s.linkCode(session, "telegram"))
	tg.waitSent(s.t, person.ID, "Telegram account to "+name)
	tg.dm(bot, person, "YES")
	tg.waitSent(s.t, person.ID, "now linked to "+name)
}

// startTelegramFleet brings up two bot-backed loops against a fake Bot API,
// with the operator already allowlisted and both bots bound to the group.
// Optional overrides merge into alpha's, then beta's, create request.
func startTelegramFleet(t *testing.T, operator user, overrides ...map[string]any) (*server, *fakeTelegram) {
	t.Helper()
	return startTelegramFleetIn(t, t.TempDir(), operator, overrides...)
}

// startTelegramFleetIn is startTelegramFleet over a data dir the caller
// holds, for a test that restarts the hub on it.
func startTelegramFleetIn(t *testing.T, dataDir string, operator user, overrides ...map[string]any) (*server, *fakeTelegram) {
	t.Helper()
	tg := startFakeTelegram(t, "alpha", "beta")
	srv := startTelegramServer(t, dataDir, tg)
	for i, name := range []string{"alpha", "beta"} {
		req := map[string]any{"tg_bot_token": name}
		if i < len(overrides) {
			for k, v := range overrides[i] {
				req[k] = v
			}
		}
		srv.createLoop(name, req)
	}

	// The operator links on alpha, which makes them an allowed sender; their
	// group message then binds every bot to the group, and reaches no loop.
	srv.allowOwner(tg, "alpha", operator)
	tg.post(groupChatID, "supergroup", "binding", operator)
	settleBindings()
	return srv, tg
}

// startTelegramServer spawns a server pointed at the stand-in Telegram API.
// The stand-in answers at once, so a call it leaves unanswered is given up
// on after five seconds rather than the production 20s.
// Every test that binds bots goes through here rather than calling
// startServerArgs itself, so the shortened bind margin and settleBindings
// cannot drift apart: the margin exists to cover Telegram's clock skew, the
// stand-in has none, and 24 rows sleeping out the production five seconds
// was a third of the suite's runtime (#136).
func startTelegramServer(t *testing.T, dataDir string, tg *fakeTelegram) *server {
	t.Helper()
	return startServerArgs(t, dataDir, "--runtime", "bare",
		"--telegram-api-base", tg.srv.URL,
		"--telegram-bind-settle-sec", "1",
		"--telegram-answer-timeout-sec", "5")
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// Nothing the operator writes in the control room leaves the hub (ADR-0032):
// not a private control_room note, and not a post to the group
// either. The group post still reaches the loops — they are in the
// conversation — while a loop's own group send, which the same fleet makes
// in answer, is mirrored as before. Until ADR-0032 this test asserted the
// opposite for the group post, which went out as "<author> (via web): ..."
// through a loop's bot.
func TestOperatorWordsStayOnTheHub(t *testing.T) {
	t.Parallel()
	operator := user{ID: 4848, First: "Operator", Username: "operator"}
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta loop words go out"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": wsAlpha})

	srv.message("alpha", "private control room note")
	srv.mustJSON("POST", "/api/loops/alpha/message",
		map[string]any{"author": "operator", "text": "operator words stay home", "destination": "group"}, nil)

	// alpha answering in the group is the proof it received the operator's
	// post, and it is also the barrier for the negative check: the mirror
	// consumes bus items in order and alpha's bot queues sends FIFO, and
	// alpha's bot is the one the old path used for an operator's post to
	// alpha. Had either of the operator's messages gone out, it would have
	// been posted before this.
	tg.waitSentFrom(t, groupChatID, "alpha", "loop words go out")
	for _, leak := range []string{"operator words stay home", "private control room note"} {
		if m, ok := tg.sentAnywhere(leak); ok {
			t.Fatalf("the operator's words reached telegram (chat %d, bot %q): %q", m.ChatID, m.Token, m.Text)
		}
	}

	// And the post is still the group's, delivered to the loop it named:
	// staying on the hub is not the same as going nowhere.
	posts := srv.activityWith("operator words stay home")
	if len(posts) != 1 {
		t.Fatalf("the operator's group post is stored %d times, want once: %s", len(posts), dump(posts))
	}
	if posts[0].Conversation != "group" {
		t.Errorf("the operator's post is in %q, want group", posts[0].Conversation)
	}
	if alpha := srv.loop("alpha").ID; !slices.Contains(posts[0].DeliveredTo, alpha) {
		t.Errorf("the operator's post was delivered to %v, want alpha among them", posts[0].DeliveredTo)
	}
}

// Unaddressed human group chatter is stored and visible but wakes no loop;
// a mention delivers to the mentioned loop alone (ADR-0025 selective wake).
func TestUnaddressedGroupChatterWakesNoLoop(t *testing.T) {
	t.Parallel()
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
// the shared conversation and is delivered to the loop, and a destination
// outside the picker's two is refused. It is not mirrored to the bound
// telegram group — nothing the operator writes leaves the hub (ADR-0032),
// which TestOperatorWordsStayOnTheHub asserts.
func TestComposerGroupDestination(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5050, First: "Operator", Username: "operator"}
	srv, _ := startTelegramFleet(t, operator)

	srv.mustJSON("POST", "/api/loops/alpha/message",
		map[string]any{"author": "operator", "text": "to the group", "destination": "group"}, nil)
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
	t.Parallel()
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
	t.Parallel()
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

// Every allowed sender holds a private conversation with a loop
// (ADR-0049): a person other than the owner is delivered as their own
// dm:<name>, the loop's answer reaches their chat and nobody else's, a
// loop cannot write first to someone who never messaged it, and two
// people's DMs never share a turn.
func TestEveryAllowedSenderHoldsAPrivateConversation(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5252, First: "Operator", Username: "operator"}
	friend := user{ID: 5353, First: "Friend", Username: "Friend"}
	stranger := user{ID: 5454, First: "Stranger", Username: "stranger"}
	// the first line answers the wake every loop takes as the hub starts
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"dm:friend","text":"answer for friend","reply_to":"$ref"}`+"\n"+
		`!send {"destination":"dm:stranger","text":"cold open"}`+"\n"+
		"!hang 3\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})
	for _, person := range []user{friend, stranger} {
		srv.allowSender(tg, "beta", person)
	}

	// turn 1: the friend's DM is theirs, and so is the answer
	tg.dm("alpha", friend, "hello alpha")
	tg.waitSent(t, friend.ID, "answer for friend")
	first := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "sent") })
	if input := strings.Join(srv.turnInputs("alpha")[first.ID], "\n"); !strings.Contains(input, "via telegram dm · dm:friend · ref:") {
		t.Fatalf("the friend's DM was not headed as their conversation: %q", input)
	}
	for _, m := range tg.sentTo(operator.ID) {
		if strings.Contains(m.Text, "answer for friend") {
			t.Fatalf("the friend's answer reached the owner: %q", m.Text)
		}
	}

	// turn 2: someone who never messaged the loop cannot be written to first
	srv.mustJSON("POST", "/api/loops/alpha/wake", nil, nil)
	refused := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.Trigger != "message" && strings.HasPrefix(tn.ResultText, "send")
	})
	if !strings.Contains(refused.ResultText, "send error: dm_unavailable") {
		t.Fatalf("a first message to someone who never wrote was not refused: %q", refused.ResultText)
	}
	for _, m := range tg.sentTo(stranger.ID) {
		if strings.Contains(m.Text, "cold open") {
			t.Fatalf("the stranger was written to first: %q", m.Text)
		}
	}

	// turn 3 holds the loop while the owner and the friend both write: each
	// DM is its own turn, never a batch of the two
	tg.dm("alpha", operator, "start hanging")
	srv.waitState("alpha", "busy", 10*time.Second)
	tg.dm("alpha", friend, "from friend")
	tg.dm("alpha", operator, "from op")
	srv.waitForMessage("from friend")
	srv.waitForMessage("from op")
	srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "from op") })
	srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "from friend") })
	for id, envelopes := range srv.turnInputs("alpha") {
		input := strings.Join(envelopes, "\n")
		if strings.Contains(input, "from friend") && strings.Contains(input, "from op") {
			t.Fatalf("turn %s was given two people's DMs: %q", id, input)
		}
	}
	for _, m := range tg.sentTo(groupChatID) {
		if strings.Contains(m.Text, "answer for") {
			t.Fatalf("a private answer surfaced in the group: %q", m.Text)
		}
	}
}

// A bot token getMe refuses is not stored, neither on a new loop nor by an
// edit, and the refusal does not echo the token back (#790). Slack's row
// is TestSlackAttachRefusals.
func TestARefusedTelegramTokenIsNotStored(t *testing.T) {
	t.Parallel()
	// shaped like a bot token, and deliberately not one
	const refused = "0000000000:AA-synthetic-refused-0000000000000000"
	tg := startFakeTelegram(t, "alpha")
	tg.refuse(refused)
	srv := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--telegram-api-base", tg.srv.URL)
	srv.createLoop("alpha", map[string]any{"tg_bot_token": "alpha"})

	for _, attempt := range []struct {
		name, method, path string
		body               map[string]any
	}{
		{"a new loop", "POST", "/api/loops", map[string]any{"name": "beta", "mission": "m", "tg_bot_token": refused}},
		{"an edit", "PATCH", "/api/loops/alpha", map[string]any{"tg_bot_token": refused}},
	} {
		resp, body := srv.do(attempt.method, attempt.path, attempt.body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "telegram token rejected") {
			t.Errorf("%s with a refused token: %d %s, want 400 telegram token rejected", attempt.name, resp.StatusCode, body)
		}
		if strings.Contains(string(body), refused) {
			t.Errorf("%s: the refusal echoes the token: %s", attempt.name, body)
		}
	}
	if resp, _ := srv.do("GET", "/api/loops/beta", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the loop a refused token was given to: %d, want 404", resp.StatusCode)
	}
	var alpha struct {
		TGBotUsername string `json:"tg_bot_username"`
	}
	srv.mustJSON("GET", "/api/loops/alpha", nil, &alpha)
	if alpha.TGBotUsername != botUsername("alpha") {
		t.Errorf("alpha's bot after a refused edit = %q, want %q kept", alpha.TGBotUsername, botUsername("alpha"))
	}
}
