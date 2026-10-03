//go:build integration

package itest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeSlack stands in for Slack, in the spirit of fakeclaude (ADR-0009):
// enough of the Web API and of Socket Mode for the paths under test, with
// synthetic tokens. apps.connections.open hands out a URL on this same
// server, where each app's Socket Mode connection lands.
type fakeSlack struct {
	srv *httptest.Server

	mu    sync.Mutex
	bots  map[string]slackBot // by bot token
	apps  map[string]bool     // app-level tokens that open a connection
	opens map[string]int      // apps.connections.open calls answered, by app token
	live  map[string]*fakeSocket
	acks  map[string][]string // envelope ids acknowledged, by app token
	posts []slackPost
	// failPosts answers that many chat.postMessage calls with a transient
	// error before taking one; -1 fails every one.
	failPosts int
	// failText fails, the same way, every post whose text contains it: how
	// a test fails one part of a long message and not the others.
	failText  string
	postCalls int
	// files are what a file_share event can point at, by id; downloads
	// counts each served, by id. uploads are the files apps shared, and
	// pending the ones whose bytes arrived before the upload completed.
	files     map[string]fakeSlackFile
	downloads map[string]int
	pending   map[string]*slackUpload
	uploads   []slackUpload
	// reacted are the reactions.add and reactions.remove calls apps made.
	reacted []slackReaction
	// updates are the chat.update calls apps made, each the post as it
	// was redrawn.
	updates []slackPost
}

// slackReaction is one reactions.add or reactions.remove an app made.
type slackReaction struct {
	Token, Channel, TS, Name string
	Removed                  bool
}

// fakeSlackFile is a file someone shared, as Slack serves it.
type fakeSlackFile struct {
	Name, Mimetype string
	Body           []byte
}

// slackUpload is one file an app shared. Posts is how many posts the fake
// had taken when the upload completed, which orders it against them.
type slackUpload struct {
	Token, Channel, ThreadTS, Title string
	Body                            []byte
	Posts                           int
}

// slackPost is one chat.postMessage, or chat.update, the fake took.
// Blocks is the Block Kit JSON it carried, "" for none.
type slackPost struct {
	Token, Channel, Text, ThreadTS, TS, Blocks string
}

// fakeSocket is one app's live Socket Mode connection.
type fakeSocket struct {
	conn   *websocket.Conn
	closed chan struct{}
}

type slackBot struct {
	UserID, Name, TeamID, TeamName string
}

func startFakeSlack(t *testing.T) *fakeSlack {
	t.Helper()
	slack := &fakeSlack{bots: map[string]slackBot{}, apps: map[string]bool{},
		opens: map[string]int{}, live: map[string]*fakeSocket{}, acks: map[string][]string{},
		files: map[string]fakeSlackFile{}, downloads: map[string]int{}, pending: map[string]*slackUpload{}}
	slack.srv = httptest.NewServer(http.HandlerFunc(slack.handle))
	t.Cleanup(slack.srv.Close)
	return slack
}

// addApp registers one Slack app: its bot token, its app-level token, and
// who the bot is.
func (slack *fakeSlack) addApp(botToken, appToken string, bot slackBot) {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	slack.bots[botToken] = bot
	slack.apps[appToken] = true
}

func (slack *fakeSlack) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/link" {
		slack.serveSocket(w, r)
		return
	}
	slack.mu.Lock()
	defer slack.mu.Unlock()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if id, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok {
		slack.serveDownload(w, token, id)
		return
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/upload/"); ok {
		upload := slack.pending[id]
		body, err := io.ReadAll(r.Body)
		if upload == nil || err != nil || int64(len(body)) != r.ContentLength {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		upload.Body = body
		_, _ = io.WriteString(w, "OK - "+strconv.Itoa(len(body)))
		return
	}
	answer := map[string]any{"ok": false, "error": "invalid_auth"}
	switch r.URL.Path {
	case "/files.info":
		if _, ok := slack.bots[token]; ok {
			id := r.FormValue("file")
			answer = map[string]any{"ok": false, "error": "file_not_found"}
			if file, ok := slack.files[id]; ok {
				answer = map[string]any{"ok": true, "file": slack.describe(id, file)}
			}
		}
	case "/files.getUploadURLExternal":
		if _, ok := slack.bots[token]; ok {
			id := fmt.Sprintf("F0UP%d", len(slack.pending)+1)
			slack.pending[id] = &slackUpload{Token: token, Title: r.FormValue("filename")}
			answer = map[string]any{"ok": true, "file_id": id, "upload_url": slack.srv.URL + "/upload/" + id}
		}
	case "/files.completeUploadExternal":
		if _, ok := slack.bots[token]; ok {
			var files []struct{ ID, Title string }
			_ = json.Unmarshal([]byte(r.FormValue("files")), &files)
			answer = map[string]any{"ok": false, "error": "invalid_arguments"}
			if len(files) == 1 && slack.pending[files[0].ID] != nil && slack.pending[files[0].ID].Body != nil {
				upload := *slack.pending[files[0].ID]
				upload.Channel, upload.ThreadTS, upload.Title = r.FormValue("channel_id"), r.FormValue("thread_ts"), files[0].Title
				upload.Posts = len(slack.posts)
				slack.uploads = append(slack.uploads, upload)
				answer = map[string]any{"ok": true, "files": []map[string]any{{"id": files[0].ID, "title": files[0].Title}}}
			}
		}
	case "/auth.test":
		if bot, ok := slack.bots[token]; ok {
			answer = map[string]any{"ok": true, "user_id": bot.UserID, "user": bot.Name,
				"team_id": bot.TeamID, "team": bot.TeamName, "bot_id": "B" + bot.UserID}
		}
	case "/users.info":
		if _, ok := slack.bots[token]; ok {
			userID := r.FormValue("user")
			answer = map[string]any{"ok": true, "user": map[string]any{"id": userID, "team_id": "T0ACME",
				"name": strings.ToLower(userID), "profile": map[string]any{"display_name": userID}}}
		}
	case "/chat.postMessage":
		if _, ok := slack.bots[token]; ok {
			slack.postCalls++
			if slack.failText != "" && strings.Contains(r.FormValue("text"), slack.failText) {
				answer = map[string]any{"ok": false, "error": "internal_error"}
				break
			}
			if slack.failPosts != 0 {
				if slack.failPosts > 0 {
					slack.failPosts--
				}
				answer = map[string]any{"ok": false, "error": "internal_error"}
				break
			}
			post := slackPost{Token: token, Channel: r.FormValue("channel"), Text: r.FormValue("text"),
				ThreadTS: r.FormValue("thread_ts"), TS: fmt.Sprintf("1727700000.%06d", len(slack.posts)+1),
				Blocks: r.FormValue("blocks")}
			slack.posts = append(slack.posts, post)
			answer = map[string]any{"ok": true, "channel": post.Channel, "ts": post.TS}
		}
	case "/chat.update":
		if _, ok := slack.bots[token]; ok {
			slack.updates = append(slack.updates, slackPost{Token: token, Channel: r.FormValue("channel"),
				Text: r.FormValue("text"), TS: r.FormValue("ts"), Blocks: r.FormValue("blocks")})
			answer = map[string]any{"ok": true, "channel": r.FormValue("channel"), "ts": r.FormValue("ts")}
		}
	case "/reactions.add", "/reactions.remove":
		if _, ok := slack.bots[token]; ok {
			slack.reacted = append(slack.reacted, slackReaction{Token: token, Channel: r.FormValue("channel"),
				TS: r.FormValue("timestamp"), Name: r.FormValue("name"), Removed: r.URL.Path == "/reactions.remove"})
			answer = map[string]any{"ok": true}
		}
	case "/conversations.open":
		if _, ok := slack.bots[token]; ok {
			answer = map[string]any{"ok": true, "channel": map[string]any{"id": "D" + r.FormValue("users")}}
		}
	case "/conversations.info":
		if _, ok := slack.bots[token]; ok {
			answer = map[string]any{"ok": true, "channel": map[string]any{"id": r.FormValue("channel"),
				"name": strings.ToLower(r.FormValue("channel"))}}
		}
	case "/apps.connections.open":
		switch {
		case slack.apps[token]:
			slack.opens[token]++
			answer = map[string]any{"ok": true,
				"url": "ws" + strings.TrimPrefix(slack.srv.URL, "http") + "/link?app=" + token}
		case strings.HasPrefix(token, "xoxb-"):
			answer = map[string]any{"ok": false, "error": "not_allowed_token_type"}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

// serveDownload serves a shared file to a bot token, and to anyone else
// Slack's sign-in page, which is what Slack answers an app without
// files:read with.
func (slack *fakeSlack) serveDownload(w http.ResponseWriter, token, id string) {
	file, ok := slack.files[id]
	if _, known := slack.bots[token]; !known || !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html>sign in</html>")
		return
	}
	slack.downloads[id]++
	w.Header().Set("Content-Type", file.Mimetype)
	_, _ = w.Write(file.Body)
}

// addFile makes a file someone shared downloadable, and returns how a
// message event describes it.
func (slack *fakeSlack) addFile(id, name, mimetype string, body []byte) map[string]any {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	file := fakeSlackFile{Name: name, Mimetype: mimetype, Body: body}
	slack.files[id] = file
	return slack.describe(id, file)
}

func (slack *fakeSlack) describe(id string, file fakeSlackFile) map[string]any {
	return map[string]any{"id": id, "name": file.Name, "mimetype": file.Mimetype, "size": len(file.Body),
		"url_private_download": slack.srv.URL + "/download/" + id}
}

// downloaded is how many times the file id was served.
func (slack *fakeSlack) downloaded(id string) int {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	return slack.downloads[id]
}

// waitUpload blocks until an app has shared a file titled title, and
// returns it.
func (slack *fakeSlack) waitUpload(t *testing.T, title string) slackUpload {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		slack.mu.Lock()
		for _, upload := range slack.uploads {
			if upload.Title == title {
				slack.mu.Unlock()
				return upload
			}
		}
		slack.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no file %q was shared", title)
	return slackUpload{}
}

// pushEvent sends one message event down app's connection as it is given.
func (slack *fakeSlack) pushEvent(t *testing.T, app string, event map[string]any) {
	t.Helper()
	slack.push(t, app, map[string]any{"type": "events_api", "envelope_id": fmt.Sprintf("%s:%v:%v", app, event["channel"], event["ts"]),
		"accepts_response_payload": false,
		"payload":                  map[string]any{"type": "event_callback", "team_id": "T0ACME", "event": event}})
}

// serveSocket is one Socket Mode connection: hello first, then whatever the
// test pushes, with every acknowledgement recorded.
func (slack *fakeSlack) serveSocket(w http.ResponseWriter, r *http.Request) {
	app := r.URL.Query().Get("app")
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	socket := &fakeSocket{conn: conn, closed: make(chan struct{})}
	defer close(socket.closed)
	hello, _ := json.Marshal(map[string]any{"type": "hello", "num_connections": 1,
		"debug_info": map[string]any{"approximate_connection_time": 18060}})
	if conn.Write(r.Context(), websocket.MessageText, hello) != nil {
		return
	}
	slack.mu.Lock()
	slack.live[app] = socket
	slack.mu.Unlock()
	defer func() {
		slack.mu.Lock()
		if slack.live[app] == socket {
			delete(slack.live, app)
		}
		slack.mu.Unlock()
	}()
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if json.Unmarshal(data, &ack) == nil && ack.EnvelopeID != "" {
			slack.mu.Lock()
			slack.acks[app] = append(slack.acks[app], ack.EnvelopeID)
			slack.mu.Unlock()
		}
	}
}

// socket waits for app's live connection.
func (slack *fakeSlack) socket(t *testing.T, app string) *fakeSocket {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		slack.mu.Lock()
		socket := slack.live[app]
		slack.mu.Unlock()
		if socket != nil {
			return socket
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("app %s never connected over Socket Mode", app)
	return nil
}

// push sends one frame down app's live connection.
func (slack *fakeSlack) push(t *testing.T, app string, msg map[string]any) *fakeSocket {
	t.Helper()
	socket := slack.socket(t, app)
	data, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := socket.conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("push to %s: %v", app, err)
	}
	return socket
}

// pushEnvelope sends an events_api envelope with a message event in it.
func (slack *fakeSlack) pushEnvelope(t *testing.T, app, envelopeID string) {
	t.Helper()
	slack.push(t, app, map[string]any{"type": "events_api", "envelope_id": envelopeID,
		"accepts_response_payload": false,
		"payload": map[string]any{"type": "event_callback",
			"event": map[string]any{"type": "message", "channel": "C0FLEET", "user": "U0HUMAN",
				"text": "hello", "ts": "1727600000.000100"}}})
}

// pushMessage sends a message event down app's connection, as Slack does
// for message.channels (channelType "channel") and message.im ("im").
func (slack *fakeSlack) pushMessage(t *testing.T, app, channelType, channel, user, text, ts string) {
	t.Helper()
	slack.pushReply(t, app, channelType, channel, user, text, ts, "")
}

// pushReply is pushMessage for a reply in the thread threadTS starts.
func (slack *fakeSlack) pushReply(t *testing.T, app, channelType, channel, user, text, ts, threadTS string) {
	t.Helper()
	event := map[string]any{"type": "message", "channel_type": channelType, "channel": channel,
		"user": user, "text": text, "ts": ts}
	if threadTS != "" {
		event["thread_ts"] = threadTS
	}
	slack.push(t, app, map[string]any{"type": "events_api", "envelope_id": app + ":" + channel + ":" + ts,
		"accepts_response_payload": false,
		"payload":                  map[string]any{"type": "event_callback", "team_id": "T0ACME", "event": event}})
}

// waitPost blocks until a post whose text contains text has been taken,
// and returns it.
func (slack *fakeSlack) waitPost(t *testing.T, text string) slackPost {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		slack.mu.Lock()
		for _, post := range slack.posts {
			if strings.Contains(post.Text, text) {
				slack.mu.Unlock()
				return post
			}
		}
		slack.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no post with %q; posts: %s", text, dump(slack.postsTaken()))
	return slackPost{}
}

func (slack *fakeSlack) postsTaken() []slackPost {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	return append([]slackPost(nil), slack.posts...)
}

// failPostsContaining fails every post whose text contains text, until the
// test clears it with "".
func (slack *fakeSlack) failPostsContaining(text string) {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	slack.failText = text
}

// postAttempts counts every chat.postMessage an app made, failed or not.
func (slack *fakeSlack) postAttempts() int {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	return slack.postCalls
}

func (slack *fakeSlack) setFailPosts(n int) {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	slack.failPosts = n
}

// waitAck blocks until app has acknowledged envelopeID.
func (slack *fakeSlack) waitAck(t *testing.T, app, envelopeID string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		slack.mu.Lock()
		acks := append([]string(nil), slack.acks[app]...)
		slack.mu.Unlock()
		for _, id := range acks {
			if id == envelopeID {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("app %s never acknowledged envelope %s", app, envelopeID)
}

// opened is how many Socket Mode URLs app has been handed.
func (slack *fakeSlack) opened(app string) int {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	return slack.opens[app]
}

func startSlackServer(t *testing.T, slack *fakeSlack) *server {
	t.Helper()
	return startServerArgs(t, t.TempDir(), "--runtime", "bare", "--slack-api-base", slack.srv.URL)
}

// Synthetic credentials for the apps these tests attach.
const (
	slackBotToken      = "xoxb-synthetic-terra"
	slackAppToken      = "xapp-synthetic-terra"
	slackOtherBotToken = "xoxb-synthetic-other-workspace"
	slackOtherAppToken = "xapp-synthetic-other-workspace"
)

var terraBot = slackBot{UserID: "U0TERRA", Name: "terra", TeamID: "T0ACME", TeamName: "Acme"}

type slackStatus struct {
	Configured  bool   `json:"configured"`
	BotUserID   string `json:"bot_user_id"`
	BotName     string `json:"bot_name"`
	TeamID      string `json:"team_id"`
	TeamName    string `json:"team_name"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Bridge      struct {
		Connected     bool   `json:"connected"`
		LastEventAt   int64  `json:"last_event_at"`
		LastError     string `json:"last_error"`
		IgnoredEvents int    `json:"ignored_events"`
	} `json:"bridge"`
}

type surfaceView struct {
	Surface        string `json:"surface"`
	HasSlackTokens bool   `json:"has_slack_tokens"`
	HasTGToken     bool   `json:"has_tg_token"`
	SlackBotUserID string `json:"slack_bot_user_id"`
}

func slackPair(appToken, botToken string) map[string]any {
	return map[string]any{"slack_app_token": appToken, "slack_bot_token": botToken}
}

// A loop is given a Slack app by pasting its two tokens, reads back as a
// Slack loop without either token ever appearing in a response, and is
// detached by clearing both (#230's control-room contract).
func TestSlackAppAttachesAndDetaches(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	srv := startSlackServer(t, slack)
	srv.createLoop("terra", nil)

	resp, body := srv.do("PATCH", "/api/loops/terra", slackPair(slackAppToken, slackBotToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attach = %d %s", resp.StatusCode, body)
	}
	for _, token := range []string{slackAppToken, slackBotToken} {
		if strings.Contains(string(body), token) {
			t.Fatalf("the attach response carries a token: %s", body)
		}
	}
	var attached surfaceView
	srv.mustJSON("GET", "/api/loops/terra", nil, &attached)
	if attached.Surface != "slack" || !attached.HasSlackTokens || attached.SlackBotUserID != "U0TERRA" {
		t.Fatalf("after attach the loop reads %+v", attached)
	}

	var status slackStatus
	srv.mustJSON("GET", "/api/loops/terra/slack/status", nil, &status)
	if !status.Configured || status.BotUserID != "U0TERRA" || status.BotName != "terra" ||
		status.TeamID != "T0ACME" || status.TeamName != "Acme" || status.ChannelID != "" {
		t.Errorf("status = %+v", status)
	}
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.Bridge.Connected })

	srv.mustJSON("PATCH", "/api/loops/terra", slackPair("", ""), nil)
	var detached surfaceView
	srv.mustJSON("GET", "/api/loops/terra", nil, &detached)
	if detached.Surface != "" || detached.HasSlackTokens || detached.SlackBotUserID != "" {
		t.Fatalf("after detach the loop reads %+v", detached)
	}

	// Given at creation, the pair lands the same way.
	overrides := slackPair(slackAppToken, slackBotToken)
	srv.createLoop("born-on-slack", overrides)
	var born surfaceView
	srv.mustJSON("GET", "/api/loops/born-on-slack", nil, &born)
	if born.Surface != "slack" || born.SlackBotUserID != "U0TERRA" {
		t.Errorf("a loop created with a Slack app reads %+v", born)
	}
}

// Every refusal the attach form keys on has its code, and none of them
// stores anything (#230).
func TestSlackAttachRefusals(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	slack.addApp(slackOtherBotToken, slackOtherAppToken,
		slackBot{UserID: "U0ELSE", Name: "else", TeamID: "T0OTHER", TeamName: "Other"})
	tg := startFakeTelegram(t, "tgbot")
	srv := startServerArgs(t, t.TempDir(), "--runtime", "bare",
		"--slack-api-base", slack.srv.URL, "--telegram-api-base", tg.srv.URL)
	srv.createLoop("terra", nil)
	srv.createLoop("on-telegram", map[string]any{"tg_bot_token": "tgbot"})
	srv.createLoop("on-slack", slackPair(slackAppToken, slackBotToken))

	for _, testCase := range []struct {
		name       string
		loop       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{"one token alone", "terra", map[string]any{"slack_bot_token": slackBotToken}, 400, ""},
		{"a bot token Slack does not know", "terra", slackPair(slackAppToken, "xoxb-synthetic-unknown"), 400, "slack_bot_token_rejected"},
		{"the bot token pasted twice", "terra", slackPair(slackBotToken, slackBotToken), 400, "slack_app_token_rejected"},
		{"an app in another workspace", "terra", slackPair(slackOtherAppToken, slackOtherBotToken), 409, "slack_workspace_mismatch"},
		{"a loop on Telegram", "on-telegram", slackPair(slackAppToken, slackBotToken), 409, "surface_in_use"},
		{"a Telegram bot for a loop on Slack", "on-slack", map[string]any{"tg_bot_token": "tgbot"}, 409, "surface_in_use"},
	} {
		resp, body := srv.do("PATCH", "/api/loops/"+testCase.loop, testCase.body)
		var refusal struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(body, &refusal)
		if resp.StatusCode != testCase.wantStatus || refusal.Code != testCase.wantCode {
			t.Errorf("%s: %d %q (%s), want %d %q", testCase.name, resp.StatusCode, refusal.Code, body,
				testCase.wantStatus, testCase.wantCode)
		}
	}

	var untouched surfaceView
	srv.mustJSON("GET", "/api/loops/terra", nil, &untouched)
	if untouched.Surface != "" || untouched.HasSlackTokens {
		t.Errorf("a refused attach stored something: %+v", untouched)
	}
	var stillTelegram surfaceView
	srv.mustJSON("GET", "/api/loops/on-telegram", nil, &stillTelegram)
	if stillTelegram.Surface != "telegram" || stillTelegram.HasSlackTokens {
		t.Errorf("the Telegram loop reads %+v", stillTelegram)
	}
}

// seedSlackSender writes a Slack sender straight into spool.db, the way
// archiveLoop writes a status: nothing registers one until the Socket Mode
// slice lands. The server must be stopped.
func seedSlackSender(t *testing.T, dataDir, userID, teamID, display, status string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO slack_senders
		(slack_user_id, team_id, username, display, status, pair_code, first_seen_via, created_at, updated_at)
		VALUES (?,?,?,?,?,'123456','dm:terra',1,1)`, userID, teamID, strings.ToLower(display), display, status); err != nil {
		t.Fatalf("seed %s: %v", userID, err)
	}
}

type slackOwnerView struct {
	OwnerSlackUserID string `json:"owner_slack_user_id"`
	OwnerUsername    string `json:"owner_username"`
}

// A Slack loop's owner is an allowed Slack sender in its workspace, chosen in
// the control room, and a sender who stops being allowed stops owning it
// (#230).
func TestSlackSendersAndOwner(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	dataDir := t.TempDir()
	srv := startServerArgs(t, dataDir, "--runtime", "bare", "--slack-api-base", slack.srv.URL)
	srv.createLoop("terra", slackPair(slackAppToken, slackBotToken))
	srv.stop()
	seedSlackSender(t, dataDir, "U0ALICE", "T0ACME", "Alice", "pending")
	seedSlackSender(t, dataDir, "U0MALLORY", "T0OTHER", "Mallory", "allowed")
	srv = startServerArgs(t, dataDir, "--runtime", "bare", "--slack-api-base", slack.srv.URL)

	var senders []struct {
		SlackUserID string `json:"slack_user_id"`
		Status      string `json:"status"`
		PairCode    string `json:"pair_code"`
	}
	srv.mustJSON("GET", "/api/slack/senders", nil, &senders)
	if len(senders) != 2 {
		t.Fatalf("senders = %+v, want the two seeded", senders)
	}

	owner := func(userID string) (int, []byte) {
		resp, body := srv.do("PUT", "/api/loops/terra/owner", map[string]any{"slack_user_id": userID})
		return resp.StatusCode, body
	}
	if status, body := owner("U0ALICE"); status != http.StatusBadRequest {
		t.Errorf("a pending sender became owner: %d %s", status, body)
	}
	if status, body := owner("U0MALLORY"); status != http.StatusBadRequest {
		t.Errorf("a sender from another workspace became owner: %d %s", status, body)
	}

	srv.mustJSON("POST", "/api/slack/senders/U0ALICE/allow", nil, nil)
	if status, body := owner("U0ALICE"); status != http.StatusOK {
		t.Fatalf("an allowed sender in the workspace could not own the loop: %d %s", status, body)
	}
	var owned slackOwnerView
	srv.mustJSON("GET", "/api/loops/terra", nil, &owned)
	if owned.OwnerSlackUserID != "U0ALICE" || owned.OwnerUsername != "Alice" {
		t.Fatalf("owner reads %+v", owned)
	}

	srv.mustJSON("POST", "/api/slack/senders/U0ALICE/block", nil, nil)
	var disowned slackOwnerView
	srv.mustJSON("GET", "/api/loops/terra", nil, &disowned)
	if disowned.OwnerSlackUserID != "" {
		t.Errorf("a blocked sender still owns the loop: %+v", disowned)
	}

	srv.mustJSON("DELETE", "/api/slack/senders/U0ALICE", nil, nil)
	if resp, body := srv.do("POST", "/api/slack/senders/U0ALICE/allow", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("allowing a deleted sender = %d %s, want 404", resp.StatusCode, body)
	}

	// A Telegram owner for a Slack loop is refused, not stored.
	if resp, body := srv.do("PUT", "/api/loops/terra/owner", map[string]any{"tg_user_id": 42}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a telegram owner on a slack loop = %d %s, want 400", resp.StatusCode, body)
	}
}

// A detached loop keeps its Slack owner for a re-attach in the same
// workspace, and loses them to an app from another: that app's bot could
// never reach them, and the owner endpoint refuses the pairing outright.
func TestSlackOwnerFollowsTheWorkspace(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	slack.addApp(slackOtherBotToken, slackOtherAppToken,
		slackBot{UserID: "U0ELSE", Name: "else", TeamID: "T0OTHER", TeamName: "Other"})
	dataDir := t.TempDir()
	srv := startServerArgs(t, dataDir, "--runtime", "bare", "--slack-api-base", slack.srv.URL)
	srv.createLoop("terra", slackPair(slackAppToken, slackBotToken))
	srv.stop()
	seedSlackSender(t, dataDir, "U0ALICE", "T0ACME", "Alice", "allowed")
	srv = startServerArgs(t, dataDir, "--runtime", "bare", "--slack-api-base", slack.srv.URL)
	srv.mustJSON("PUT", "/api/loops/terra/owner", map[string]any{"slack_user_id": "U0ALICE"}, nil)

	ownerAfter := func(pair map[string]any) string {
		t.Helper()
		srv.mustJSON("PATCH", "/api/loops/terra", pair, nil)
		var view slackOwnerView
		srv.mustJSON("GET", "/api/loops/terra", nil, &view)
		return view.OwnerSlackUserID
	}
	if got := ownerAfter(slackPair("", "")); got != "U0ALICE" {
		t.Fatalf("detaching dropped the owner: %q", got)
	}
	if got := ownerAfter(slackPair(slackAppToken, slackBotToken)); got != "U0ALICE" {
		t.Fatalf("re-attaching in the same workspace dropped the owner: %q", got)
	}
	ownerAfter(slackPair("", ""))
	if got := ownerAfter(slackPair(slackOtherAppToken, slackOtherBotToken)); got != "" {
		t.Fatalf("an app from another workspace kept owner %q", got)
	}
}

// waitSlackLink polls a loop's Slack status until pred holds.
func (s *server) waitSlackLink(name string, pred func(slackStatus) bool) slackStatus {
	s.t.Helper()
	var status slackStatus
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		s.mustJSON("GET", "/api/loops/"+name+"/slack/status", nil, &status)
		if pred(status) {
			return status
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("%s's Slack link never got there: %+v", name, status)
	return status
}

// A loop with a Slack app holds a Socket Mode connection (#230) and keeps
// it: every envelope is acknowledged, or Slack would deliver it again and
// wake the loop twice for one message; a refresh Slack asks for is taken
// at once; a connection the server drops without a disconnect is dialled
// again; and detaching the app closes it. A connection that dies silently,
// which only the ping notices, is tier 1's (TestUnansweredPingDropsTheLink).
func TestSlackSocketModeLinkIsKept(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	srv := startSlackServer(t, slack)
	srv.createLoop("terra", slackPair(slackAppToken, slackBotToken))
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.Bridge.Connected })

	slack.pushEnvelope(t, slackAppToken, "env-1")
	slack.waitAck(t, slackAppToken, "env-1")
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.Bridge.LastEventAt != 0 })

	// Validating the app at attach asked for a URL too, so count from here.
	before := slack.opened(slackAppToken)
	first := slack.push(t, slackAppToken, map[string]any{"type": "disconnect", "reason": "refresh_requested"})
	<-first.closed
	slack.pushEnvelope(t, slackAppToken, "env-2")
	slack.waitAck(t, slackAppToken, "env-2")
	if n := slack.opened(slackAppToken) - before; n != 1 {
		t.Fatalf("a refresh took %d connection URLs, want 1", n)
	}

	// The server drops the connection without a disconnect frame.
	dead := slack.socket(t, slackAppToken)
	dead.conn.CloseNow()
	<-dead.closed
	slack.pushEnvelope(t, slackAppToken, "env-3")
	slack.waitAck(t, slackAppToken, "env-3")

	last := slack.socket(t, slackAppToken)
	srv.mustJSON("PATCH", "/api/loops/terra", slackPair("", ""), nil)
	select {
	case <-last.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("detaching the app left its Socket Mode connection open")
	}
	srv.waitSlackLink("terra", func(link slackStatus) bool { return !link.Bridge.Connected })
}

// Slack disables an app's Socket Mode with a disconnect that says so, and
// says nothing when the operator turns it back on. So the link goes down,
// says why and what brings it back, and checks again only at the longest
// backoff, minutes away, rather than dialling at once. That the check finds
// it re-enabled is tier 1's (TestDisabledLinkComesBackWhenReenabled): a
// five-minute wait has no place here.
func TestSlackDisabledLinkWaitsToBeReenabled(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	srv := startSlackServer(t, slack)
	srv.createLoop("terra", slackPair(slackAppToken, slackBotToken))
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.Bridge.Connected })

	before := slack.opened(slackAppToken)
	socket := slack.push(t, slackAppToken, map[string]any{"type": "disconnect", "reason": "link_disabled"})
	<-socket.closed
	status := srv.waitSlackLink("terra", func(link slackStatus) bool { return !link.Bridge.Connected })
	for _, want := range []string{"disabled Socket Mode", "Turn it back on"} {
		if !strings.Contains(status.Bridge.LastError, want) {
			t.Errorf("last_error = %q, want it to carry %q", status.Bridge.LastError, want)
		}
	}
	time.Sleep(3 * time.Second)
	if n := slack.opened(slackAppToken) - before; n != 0 {
		t.Fatalf("a disabled link asked for %d more connection URLs within 3s, want none", n)
	}
}
