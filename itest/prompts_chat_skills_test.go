//go:build integration

package itest

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// A bot's skills aren't installed by an import, and the loop isn't told
// it has them (#1332). The draft lists each skill the bot names as not
// installed, with the reason, and the mission says the bot was built to
// use them and they aren't installed. The loop created from that draft
// runs with that sentence, and no line claiming the skills.
func TestAnImportedBotsSkillsAreSaidToBeMissing(t *testing.T) {
	t.Parallel()
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--prompts-chat-base", startFakePromptsChat(t))

	var draft struct {
		Name    string `json:"name"`
		Mission string `json:"mission"`
		Skills  []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Installed   bool   `json:"installed"`
			Reason      string `json:"reason"`
		} `json:"skills"`
	}
	hub.mustJSON("GET", "/api/import/prompts-chat?url="+url.QueryEscape("https://prompts.chat/bots/cfixture0001_release-notes-scribe"), nil, &draft)
	if len(draft.Skills) != 1 || draft.Skills[0].Name != "Changelog style" || draft.Skills[0].Description != "Keep a Changelog, by area." ||
		draft.Skills[0].Installed || draft.Skills[0].Reason != "prompts.chat publishes no files for it" {
		t.Fatalf("the draft's skills = %+v; want Changelog style, its description on one line, not installed, with the reason", draft.Skills)
	}
	const missing = "This bot was built to use these skills, which aren't installed:\n- Changelog style: Keep a Changelog, by area."
	if !strings.Contains(draft.Mission, missing) || strings.Contains(draft.Mission, "Skills:") {
		t.Fatalf("the drafted mission doesn't say the skills are missing, or still claims them:\n%s", draft.Mission)
	}

	hub.createLoop(draft.Name, map[string]any{"mission": draft.Mission, "runtime": "bare"})
	hub.scriptLoop(draft.Name, "!sysprompt")
	hub.message(draft.Name, "what is your mission?")
	prompt := hub.waitTurn(draft.Name, 30*time.Second, func(done turn) bool {
		return strings.Contains(done.ResultText, "You draft release notes")
	}).ResultText
	if !strings.Contains(prompt, missing) || strings.Contains(prompt, "Skills:") {
		t.Errorf("the loop's system prompt doesn't say its skills are missing, or claims them:\n%s", prompt)
	}
}
