//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A loop's name is what a group mention of it routes by, so the hub
// refuses one a mention already means (#1273): another loop's bot
// username, or the username of a person it allows, ignoring case. A bot
// whose username is another loop's name is refused too, at create and
// when a token is set. A team draft suffixes those names rather than
// drafting a create the hub would refuse.
func TestALoopNameAMentionMeansIsRefused(t *testing.T) {
	t.Parallel()
	person := user{ID: 7373, First: "Planner", Username: "Planner"}
	hub, telegram := startTelegramFleetArgs(t, t.TempDir(), []string{"--prompts-chat-base", startFakePromptsChat(t)}, person)

	refused := func(what, method, path string, body map[string]any, whose string) {
		t.Helper()
		resp, raw := hub.do(method, path, body)
		var answer struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &answer)
		if resp.StatusCode != http.StatusConflict || answer.Code != "name_in_use" || !strings.Contains(answer.Error, whose) {
			t.Errorf("%s: %d %s; want 409 name_in_use naming %q", what, resp.StatusCode, raw, whose)
		}
	}
	refused("a loop named like alpha's bot", "POST", "/api/loops",
		map[string]any{"name": "alpha_bot", "mission": "m"}, "the loop alpha's bot")
	refused("a loop named like the person", "POST", "/api/loops",
		map[string]any{"name": "planner", "mission": "m"}, "Telegram username of a person the hub allows")

	hub.createLoop("gamma_bot", nil)
	telegram.addBot("gamma")
	refused("a loop created with a bot named like the loop gamma_bot", "POST", "/api/loops",
		map[string]any{"name": "delta", "mission": "m", "tg_bot_token": "gamma"}, "the loop gamma_bot")
	refused("beta given a bot named like the loop gamma_bot", "PATCH", "/api/loops/beta",
		map[string]any{"tg_bot_token": "gamma"}, "the loop gamma_bot")
	if bot := hub.loop("beta").TGBotUsername; bot != botUsername("beta") {
		t.Errorf("beta's bot after the refused token: %q; want %q", bot, botUsername("beta"))
	}
	for _, name := range hub.loopNames() {
		if name == "alpha_bot" || name == "planner" || name == "delta" {
			t.Errorf("a refused loop was made: %s", name)
		}
	}

	var draft struct {
		Loops []struct {
			Name string `json:"name"`
		} `json:"loops"`
	}
	hub.mustJSON("GET", "/api/import/prompts-chat/team?url="+url.QueryEscape("https://prompts.chat/bots/teams/launch-crew"), nil, &draft)
	var drafted []string
	for _, loop := range draft.Loops {
		drafted = append(drafted, loop.Name)
	}
	if strings.Join(drafted, " ") != "planner-2 builder qa" {
		t.Errorf("the team draft names %q; want planner-2, as the person is @Planner", drafted)
	}
}
