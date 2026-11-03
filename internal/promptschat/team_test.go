package promptschat

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// teamFixture answers the fixture team at /api/bots/teams/launch-crew and
// its bots at their pages, and records the paths asked for. Any other
// path is a 404.
type teamFixture struct {
	asked []string
	// swap replaces the bytes answered at a path.
	swap map[string]string
}

func (fixture *teamFixture) Fetch(_ context.Context, path string) ([]byte, error) {
	fixture.asked = append(fixture.asked, path)
	if body, ok := fixture.swap[path]; ok {
		return []byte(body), nil
	}
	file := map[string]string{
		"/api/bots/teams/launch-crew":   "team.json",
		"/bots/cfixa_plan-keeper.json":  "bot-cfixa.json",
		"/bots/cfixb_page-builder.json": "bot-cfixb.json",
		"/bots/cfixc_check-runner.json": "bot-cfixc.json",
	}[path]
	if file == "" {
		return nil, ErrNotFound
	}
	return os.ReadFile("testdata/" + file)
}

func takenFrom(names ...string) func(string) bool {
	return func(name string) bool {
		for _, taken := range names {
			if taken == name {
				return true
			}
		}
		return false
	}
}

// TestATeamBecomesLoopsAndAChannel: a team makes one loop per member,
// named from its role, a channel named after the team, and missions that
// end with the channel and every handoff that touches the loop, by the
// other loops' names.
func TestATeamBecomesLoopsAndAChannel(t *testing.T) {
	fixture := &teamFixture{}
	made, err := ImportTeam(context.Background(), fixture, "https://prompts.chat/bots/teams/launch-crew", takenFrom("planner"), takenFrom())
	if err != nil {
		t.Fatal(err)
	}
	wantAsked := []string{"/api/bots/teams/launch-crew", "/bots/cfixa_plan-keeper.json", "/bots/cfixb_page-builder.json", "/bots/cfixc_check-runner.json"}
	if !reflect.DeepEqual(fixture.asked, wantAsked) {
		t.Errorf("fetched %q; want %q", fixture.asked, wantAsked)
	}
	if made.Channel != "launch-crew" || made.Description != "A synthetic team for Spool's tests." || made.Source != "https://prompts.chat/bots/teams/launch-crew" {
		t.Errorf("channel %q, description %q, source %q", made.Channel, made.Description, made.Source)
	}
	var names, roles []string
	for _, loop := range made.Loops {
		names, roles = append(names, loop.Name), append(roles, loop.Role)
	}
	if want := []string{"planner-2", "builder", "qa"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %q; want %q, planner taken on the hub", names, want)
	}
	if want := []string{"Planner", "Builder", "QA"}; !reflect.DeepEqual(roles, want) {
		t.Errorf("roles = %q; want %q", roles, want)
	}
	wantPlanner := "Plan the launch.\n\nImported from https://prompts.chat/bots/cfixa_plan-keeper.\n\n" +
		"You are in the channel launch-crew with @builder and @qa.\nYour handoffs:\n- to @builder: Hand the approved plan\n- from @qa"
	if made.Loops[0].Mission != wantPlanner {
		t.Errorf("planner's mission = %q\nwant %q", made.Loops[0].Mission, wantPlanner)
	}
	if !strings.HasSuffix(made.Loops[1].Mission, "\n\nYou are in the channel launch-crew with @planner-2 and @qa.\nYour handoffs:\n"+
		"- from @planner-2: Hand the approved plan\n- to @qa: Send the build for checks") {
		t.Errorf("builder's mission = %q", made.Loops[1].Mission)
	}
}

// TestTeamNamesAreUnique: a channel name taken on the hub, and two
// members with one role, are suffixed; a role that leaves no name takes
// its bot's.
func TestTeamNamesAreUnique(t *testing.T) {
	fixture := &teamFixture{swap: map[string]string{"/api/bots/teams/launch-crew": `{"slug":"launch-crew","members":[
		{"id":"cfixa","slug":"plan-keeper","role":"Lead"},
		{"id":"cfixb","slug":"page-builder","role":"Lead"},
		{"id":"cfixc","slug":"check-runner","role":"!"}]}`}}
	made, err := ImportTeam(context.Background(), fixture, "https://prompts.chat/bots/teams/launch-crew", takenFrom(), takenFrom("launch-crew", "launch-crew-2"))
	if err != nil {
		t.Fatal(err)
	}
	if made.Channel != "launch-crew-3" {
		t.Errorf("channel = %q; want launch-crew-3", made.Channel)
	}
	var names []string
	for _, loop := range made.Loops {
		names = append(names, loop.Name)
	}
	if want := []string{"lead", "lead-2", "check-runner"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %q; want %q", names, want)
	}
}

// TestATeamThatCantBeImportedIsRefused: another host, a bot's page, a
// missing team, a team with no members or too many, a member with a bad
// id, a missing member bot and a handoff to a bot the team lacks are
// each refused with a reason; a failed fetch is not a refusal.
func TestATeamThatCantBeImportedIsRefused(t *testing.T) {
	const teamURL = "https://prompts.chat/bots/teams/launch-crew"
	many := `{"slug":"x","members":[` + strings.Repeat(`{"id":"cfixa","slug":"plan-keeper","role":"r"},`, maxMembers) + `{"id":"cfixa","slug":"plan-keeper","role":"r"}]}`
	for _, test := range []struct {
		raw, swap, reason string
	}{
		{"https://example.com/bots/teams/launch-crew", "", "teams from prompts.chat only"},
		{"https://prompts.chat/bots/cfixa_plan-keeper", "", "isn't a team"},
		{"https://prompts.chat/bots/teams/nobody", "", "no team at that address"},
		{teamURL, `{"slug":"x","members":[]}`, "no members"},
		{teamURL, `<!doctype html>`, "no members"},
		{teamURL, many, "at most 12"},
		{teamURL, `{"slug":"x","members":[{"id":"../x","slug":"y","role":"r"}]}`, "has no page for"},
		{teamURL, `{"slug":"x","members":[{"id":"cnone","slug":"gone","role":"r"}]}`, "doesn't have"},
		{teamURL, `{"slug":"x","members":[{"id":"cfixa","slug":"plan-keeper","role":"a"},{"id":"cfixa","slug":"plan-keeper","role":"b"}]}`, "one bot twice"},
		{teamURL, `{"slug":"x","members":[{"id":"cfixa","slug":"plan-keeper","role":"a"}],"handoffs":[{"from":"cfixa","to":"cother","label":"x"}]}`, "doesn't have"},
	} {
		fixture := &teamFixture{}
		if test.swap != "" {
			fixture.swap = map[string]string{"/api/bots/teams/launch-crew": test.swap}
		}
		_, err := ImportTeam(context.Background(), fixture, test.raw, takenFrom(), takenFrom())
		var refusal *Refusal
		if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, test.reason) {
			t.Errorf("ImportTeam(%q, %.40q) = %v; want a refusal saying %q", test.raw, test.swap, err, test.reason)
		}
	}
	failed := errors.New("connection reset")
	_, err := ImportTeam(context.Background(), failingFetcher{failed}, teamURL, takenFrom(), takenFrom())
	var refusal *Refusal
	if !errors.Is(err, failed) || errors.As(err, &refusal) {
		t.Errorf("ImportTeam on a failed fetch = %v; want that failure, not a refusal", err)
	}
}

// failingFetcher fails every fetch with its error.
type failingFetcher struct{ err error }

func (failing failingFetcher) Fetch(context.Context, string) ([]byte, error) { return nil, failing.err }

// TestAChannelNameFromASlug: a team's slug becomes a valid channel name.
func TestAChannelNameFromASlug(t *testing.T) {
	for slug, want := range map[string]string{
		"launch-crew":            "launch-crew",
		"Launch_Crew!":           "launch-crew",
		"--x--":                  "x",
		strings.Repeat("ab", 20): strings.Repeat("ab", 16),
		"!!!":                    "imported-team",
	} {
		if got := channelName(slug, slug); got != want {
			t.Errorf("channelName(%q) = %q; want %q", slug, got, want)
		}
	}
}

// TestALoneMemberHasNoHandoffs: a team of one names its channel only.
func TestALoneMemberHasNoHandoffs(t *testing.T) {
	fixture := &teamFixture{swap: map[string]string{"/api/bots/teams/launch-crew": `{"slug":"solo","members":[{"id":"cfixa","slug":"plan-keeper","role":"Lead"}]}`}}
	made, err := ImportTeam(context.Background(), fixture, "https://prompts.chat/bots/teams/launch-crew", takenFrom(), takenFrom())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(made.Loops[0].Mission, "\n\nYou are in the channel solo.") {
		t.Errorf("mission = %q", made.Loops[0].Mission)
	}
}
