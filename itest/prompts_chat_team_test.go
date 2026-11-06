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
// and a channel named after the team; created as read, the loops start in
// that channel with missions that end with their handoffs by the other
// loops' names. A team naming a bot prompts.chat doesn't have drafts
// nothing, and a create that fails on its last loop leaves none of them.
func TestATeamImportMakesItsLoopsAndChannel(t *testing.T) {
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

	// the loop runs with the mission the draft wrote
	hub.scriptLoop("builder", "!sysprompt")
	hub.message("builder", "what is your mission?")
	prompt := hub.waitTurn("builder", 30*time.Second, func(done turn) bool {
		return strings.Contains(done.ResultText, "Build the pages.")
	}).ResultText
	for _, want := range []string{
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
