//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// startFakePromptsChat serves the promptschat package's fixture team and
// its bots, and its fixture bot, as prompts.chat would, plus a team
// half-crew whose second bot is missing, and answers 404 for anything
// else.
func startFakePromptsChat(t *testing.T) string {
	t.Helper()
	fixtures := map[string]string{
		"/api/bots/teams/launch-crew":                  "team.json",
		"/bots/cfixa_plan-keeper.json":                 "bot-cfixa.json",
		"/bots/cfixb_page-builder.json":                "bot-cfixb.json",
		"/bots/cfixc_check-runner.json":                "bot-cfixc.json",
		"/bots/cfixture0001_release-notes-scribe.json": "bot.json",
	}
	site := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/bots/teams/half-crew" {
			_, _ = writer.Write([]byte(`{"slug":"half-crew","members":[{"id":"cfixa","slug":"plan-keeper","role":"Lead"},{"id":"cgone","slug":"gone","role":"Ghost"}]}`))
			return
		}
		file, ok := fixtures[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		body, err := os.ReadFile(filepath.Join("..", "internal", "promptschat", "testdata", file))
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(body)
	}))
	t.Cleanup(site.Close)
	return site.URL
}

// An admin imports a prompts.chat team (#1265): the draft names one loop
// per member, from its role, suffixed where the hub has the name already,
// and a channel named after the team; created as read, the hub has a team
// of that name (#1556) whose home is that channel, and the loops start in
// it, reading their team, with missions that end with their handoffs by
// the other loops' names. A team naming a bot prompts.chat doesn't have
// drafts nothing, and a create refused for a taken team, channel or loop
// name leaves no team, channel or loop.
func TestATeamImportMakesATeam(t *testing.T) {
	t.Parallel()
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--prompts-chat-base", startFakePromptsChat(t))
	hub.createLoop("qa", nil)

	var draft struct {
		Channel     string `json:"channel"`
		Description string `json:"description"`
		Loops       []struct {
			Name     string `json:"name"`
			Mission  string `json:"mission"`
			Handoffs []struct {
				Direction string `json:"direction"`
				Loop      string `json:"loop"`
				Label     string `json:"label"`
			} `json:"handoffs"`
		} `json:"loops"`
	}
	hub.mustJSON("GET", "/api/import/prompts-chat/team?url="+url.QueryEscape("https://prompts.chat/bots/teams/launch-crew"), nil, &draft)
	var drafted []string
	for _, loop := range draft.Loops {
		drafted = append(drafted, loop.Name)
	}
	if draft.Channel != "launch-crew" || strings.Join(drafted, " ") != "planner builder qa-2" {
		t.Fatalf("the draft names channel %q and loops %q; want launch-crew and planner, builder, qa-2", draft.Channel, drafted)
	}
	// each handoff comes as fields too, by the draft's names (#1266)
	var builderHandoffs []string
	for _, handoff := range draft.Loops[1].Handoffs {
		builderHandoffs = append(builderHandoffs, handoff.Direction+" "+handoff.Loop+": "+handoff.Label)
	}
	if strings.Join(builderHandoffs, "; ") != "from planner: Hand the approved plan; to qa-2: Send the build for checks" {
		t.Errorf("builder's handoffs = %q; want from planner and to qa-2, labelled", builderHandoffs)
	}
	if count := len(hub.loopNames()); count != 1 {
		t.Fatalf("the draft made loops: %d on the hub, want only qa", count)
	}

	var made struct {
		Team    string     `json:"team"`
		Channel string     `json:"channel"`
		Loops   []loopView `json:"loops"`
	}
	resp, body := hub.do("POST", "/api/import/prompts-chat/team", draft)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating the team: %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &made); err != nil {
		t.Fatal(err)
	}
	var channel struct {
		Description string   `json:"description"`
		Loops       []string `json:"loops"`
	}
	hub.mustJSON("GET", "/api/channels/launch-crew", nil, &channel)
	if strings.Join(channel.Loops, " ") != "builder planner qa-2" || channel.Description != "A synthetic team for Spool's tests." {
		t.Fatalf("the channel holds %q with description %q; want the team's three loops and its description", channel.Loops, channel.Description)
	}
	if made.Team != "launch-crew" || made.Channel != "launch-crew" {
		t.Fatalf("the create answered team %q, channel %q; want launch-crew for both", made.Team, made.Channel)
	}
	var team struct {
		Loops       []string `json:"loops"`
		HomeChannel string   `json:"home_channel"`
		Description string   `json:"description"`
	}
	hub.mustJSON("GET", "/api/teams/launch-crew", nil, &team)
	if strings.Join(team.Loops, " ") != "builder planner qa-2" || team.HomeChannel != "launch-crew" || team.Description != "A synthetic team for Spool's tests." {
		t.Fatalf("the team = %+v; want the three loops, at home in launch-crew, described", team)
	}

	// the loop runs with the mission the draft wrote
	hub.scriptLoop("builder", "!sysprompt")
	hub.message("builder", "what is your mission?")
	prompt := hub.waitTurn("builder", 30*time.Second, func(done turn) bool {
		return strings.Contains(done.ResultText, "Build the pages.")
	}).ResultText
	for _, want := range []string{
		"- You are in the team launch-crew",
		"You are in the channel launch-crew with @planner and @qa-2.",
		"- from @planner: Hand the approved plan",
		"- to @qa-2: Send the build for checks",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("builder's system prompt is missing %q:\n%s", want, prompt)
		}
	}

	// a team naming a missing bot drafts nothing
	resp, body = hub.do("GET", "/api/import/prompts-chat/team?url="+url.QueryEscape("https://prompts.chat/bots/teams/half-crew"), nil)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "import_refused") {
		t.Fatalf("drafting a team with a missing bot: %d %s; want 400 import_refused", resp.StatusCode, body)
	}

	// a create whose last loop's name is taken stores the first two, then
	// removes them
	before := hub.loopNames()
	resp, body = hub.do("POST", "/api/import/prompts-chat/team", map[string]any{
		"channel": "second-crew",
		"loops":   []map[string]string{{"name": "lead", "mission": "Lead."}, {"name": "aide", "mission": "Help."}, {"name": "qa", "mission": "Check."}},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("creating a team over a taken name: %d %s; want 409", resp.StatusCode, body)
	}
	if after := hub.loopNames(); strings.Join(after, " ") != strings.Join(before, " ") {
		t.Fatalf("a failed create left loops %q; had %q", after, before)
	}
	if resp, _ := hub.do("GET", "/api/channels/second-crew", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a failed create left its channel: %d", resp.StatusCode)
	}
	if resp, _ := hub.do("GET", "/api/teams/second-crew", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a failed create left its team: %d", resp.StatusCode)
	}

	// a team or channel of the name asked for is refused by name, and
	// nothing is made
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "taken-team", "home_channel": "taken-home"}, nil)
	members := []map[string]string{{"name": "lead", "mission": "Lead."}, {"name": "aide", "mission": "Help."}}
	for _, refused := range []struct {
		body       map[string]any
		code, name string
	}{
		{map[string]any{"channel": "fresh", "team": "taken-team", "loops": members}, "team_exists", "taken-team"},
		{map[string]any{"channel": "launch-crew", "team": "fresh", "loops": members}, "channel_exists", "launch-crew"},
	} {
		resp, body := hub.do("POST", "/api/import/prompts-chat/team", refused.body)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), refused.code) || !strings.Contains(string(body), refused.name) {
			t.Errorf("creating %v: %d %s; want 409 %s naming %s", refused.body, resp.StatusCode, body, refused.code, refused.name)
		}
	}
	if after := hub.loopNames(); strings.Join(after, " ") != strings.Join(before, " ") {
		t.Fatalf("a refused create left loops %q; had %q", after, before)
	}
	for _, path := range []string{"/api/teams/fresh", "/api/channels/fresh"} {
		if resp, _ := hub.do("GET", path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("a refused create left %s: %d", path, resp.StatusCode)
		}
	}
}

// A draft names its channel past the names of the hub's teams as well as
// its channels, so the team its create names after the channel is free
// (#1556).
func TestATeamDraftSkipsATeamsName(t *testing.T) {
	t.Parallel()
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--prompts-chat-base", startFakePromptsChat(t))
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "launch-crew", "home_channel": "elsewhere"}, nil)
	var draft struct {
		Channel string `json:"channel"`
	}
	hub.mustJSON("GET", "/api/import/prompts-chat/team?url="+url.QueryEscape("https://prompts.chat/bots/teams/launch-crew"), nil, &draft)
	if draft.Channel != "launch-crew-2" {
		t.Errorf("the draft names channel %q with a team launch-crew on the hub; want launch-crew-2", draft.Channel)
	}
}

// loopNames is the names of the hub's loops, sorted.
func (hub *server) loopNames() []string {
	hub.t.Helper()
	var loops []loopView
	hub.mustJSON("GET", "/api/loops", nil, &loops)
	var names []string
	for _, loop := range loops {
		names = append(names, loop.Name)
	}
	sort.Strings(names)
	return names
}
