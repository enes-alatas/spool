//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeSlack stands in for the Slack Web API, in the spirit of fakeclaude
// (ADR-0009): enough of it for the paths under test, with synthetic tokens.
// Socket Mode is not here yet; it arrives with the slice that connects.
type fakeSlack struct {
	srv *httptest.Server

	mu   sync.Mutex
	bots map[string]slackBot // by bot token
	apps map[string]bool     // app-level tokens that open a connection
}

type slackBot struct {
	UserID, Name, TeamID, TeamName string
}

func startFakeSlack(t *testing.T) *fakeSlack {
	t.Helper()
	slack := &fakeSlack{bots: map[string]slackBot{}, apps: map[string]bool{}}
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
	slack.mu.Lock()
	defer slack.mu.Unlock()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	answer := map[string]any{"ok": false, "error": "invalid_auth"}
	switch r.URL.Path {
	case "/auth.test":
		if bot, ok := slack.bots[token]; ok {
			answer = map[string]any{"ok": true, "user_id": bot.UserID, "user": bot.Name,
				"team_id": bot.TeamID, "team": bot.TeamName, "bot_id": "B" + bot.UserID}
		}
	case "/apps.connections.open":
		switch {
		case slack.apps[token]:
			answer = map[string]any{"ok": true, "url": "wss://example.invalid/link"}
		case strings.HasPrefix(token, "xoxb-"):
			answer = map[string]any{"ok": false, "error": "not_allowed_token_type"}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
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
	Configured bool   `json:"configured"`
	BotUserID  string `json:"bot_user_id"`
	BotName    string `json:"bot_name"`
	TeamID     string `json:"team_id"`
	TeamName   string `json:"team_name"`
	ChannelID  string `json:"channel_id"`
	Bridge     struct {
		Connected bool `json:"connected"`
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
	if status.Bridge.Connected {
		t.Error("the status claims a connection, and nothing connects to Slack yet")
	}

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
