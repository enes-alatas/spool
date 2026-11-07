package promptschat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// skillSite answers a bot at /bots/cskills_skilled.json and each prompt
// in prompts at /api/prompts/<id>, and records every path asked for.
type skillSite struct {
	bot     bot
	prompts map[string]prompt
	failing map[string]bool
	asked   []string
}

func (site *skillSite) Fetch(_ context.Context, path string) ([]byte, error) {
	site.asked = append(site.asked, path)
	if path == "/bots/cskills_skilled.json" {
		return json.Marshal(site.bot)
	}
	id, ok := strings.CutPrefix(path, "/api/prompts/")
	if !ok {
		return nil, ErrNotFound
	}
	if site.failing[id] {
		return nil, errors.New("connection reset")
	}
	read, ok := site.prompts[id]
	if !ok {
		return nil, ErrNotFound
	}
	return json.Marshal(read)
}

// skillBot is a bot naming skills with these sources, named s0, s1, …
func skillBot(sources ...string) bot {
	made := bot{Slug: "skilled", Instructions: "You review things."}
	for index, source := range sources {
		made.Skills = append(made.Skills, skill{Name: fmt.Sprintf("s%d", index), Description: "d", Source: source})
	}
	return made
}

func skillContent(files ...string) string {
	content := "---\nname: x\n---\nDo the thing."
	for _, path := range files {
		content += "\x1fFILE:" + path + "\x1e\n" + "text of " + path
	}
	return content
}

const linkA = "https://prompts.chat/prompts/cmvw320te0001l3089ivvu448_http-headers"

func importSkills(t *testing.T, site *skillSite) Draft {
	t.Helper()
	draft, err := Import(context.Background(), site, "https://prompts.chat/bots/cskills_skilled")
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

// A bot naming a skill whose source is a prompts.chat skill's page gets a
// draft carrying that skill's files, split at the FILE: separators and
// held to the text rules, in a folder the hub made from the prompt's
// slug. The mission doesn't name an installed skill.
func TestALinkedSkillIsDrafted(t *testing.T) {
	site := &skillSite{
		bot: skillBot(linkA, ""),
		prompts: map[string]prompt{"cmvw320te0001l3089ivvu448": {Type: "SKILL", Slug: "HTTP Headers_Checker",
			Content: skillContent("references/rules.md", "examples/a.txt") + "\u200b\u202e"}},
	}
	draft := importSkills(t, site)
	if want := []string{"/bots/cskills_skilled.json", "/api/prompts/cmvw320te0001l3089ivvu448"}; !reflect.DeepEqual(site.asked, want) {
		t.Errorf("fetched %q; want %q", site.asked, want)
	}
	want := SkillHint{Name: "s0", Description: "d", Installed: true, Source: linkA, Dir: "http-headers-checker", Files: []SkillFile{
		{Path: "SKILL.md", Content: "---\nname: x\n---\nDo the thing."},
		{Path: "references/rules.md", Content: "text of references/rules.md"},
		{Path: "examples/a.txt", Content: "text of examples/a.txt"},
	}}
	if len(draft.Skills) != 2 || !reflect.DeepEqual(draft.Skills[0], want) {
		t.Fatalf("skills = %+v; want first %+v", draft.Skills, want)
	}
	if draft.Skills[1].Installed || draft.Skills[1].Reason != notPublished {
		t.Errorf("an empty source = %+v; want not installed, %q", draft.Skills[1], notPublished)
	}
	if strings.Contains(draft.Mission, "- s0") || !strings.Contains(draft.Mission, "aren't installed:\n- s1: d") {
		t.Errorf("the mission names the installed skill, or not the missing one:\n%s", draft.Mission)
	}
}

// Only a prompts.chat skill's page by exact form is a link: any other
// source is not installed and nothing is fetched for it.
func TestOnlyAnExactSkillLinkIsFetched(t *testing.T) {
	for _, source := range []string{
		"official/research/arxiv",
		"http://prompts.chat/prompts/cabc_x",
		"https://prompts.chat.evil.example/prompts/cabc_x",
		"https://evil.example/prompts/cabc_x",
		"https://prompts.chat:443/prompts/cabc_x",
		"https://user@prompts.chat/prompts/cabc_x",
		"https://PROMPTS.CHAT/prompts/cabc_x",
		"https://prompts.chat/prompts/cabc_x?next=https://evil.example",
		"https://prompts.chat/prompts/cabc_x#f",
		"https://prompts.chat/prompts/cabc",
		"https://prompts.chat/prompts/cabc_x/",
		"https://prompts.chat/prompts/../api/prompts/cabc_x",
		"https://prompts.chat/prompts/c%61bc_x",
		"https://prompts.chat/bots/cabc_x",
		"https://prompts.chat/prompts/CABC_x",
		"https://prompts.chat/prompts/cabc_x/SKILL.md",
	} {
		site := &skillSite{bot: skillBot(source), prompts: map[string]prompt{"cabc": {Type: "SKILL", Slug: "x", Content: "y"}}}
		draft := importSkills(t, site)
		if len(site.asked) != 1 || draft.Skills[0].Installed || draft.Skills[0].Reason != notPublished || draft.Skills[0].Source != "" {
			t.Errorf("source %q: fetched %q, skill %+v; want only the bot fetched and %q", source, site.asked, draft.Skills[0], notPublished)
		}
	}
}

// A link to a prompt that isn't a skill, or that prompts.chat doesn't
// have or doesn't answer for, is not installed; the import goes on.
func TestALinkThatIsNoSkillIsNotInstalled(t *testing.T) {
	site := &skillSite{
		bot: skillBot(
			"https://prompts.chat/prompts/ctext_a",
			"https://prompts.chat/prompts/cgone_b",
			"https://prompts.chat/prompts/cdown_c",
			"https://prompts.chat/prompts/cempty_d",
		),
		prompts: map[string]prompt{
			"ctext":  {Type: "TEXT", Slug: "a", Content: "hello"},
			"cempty": {Type: "SKILL", Slug: "d", Content: " \u200b "},
		},
		failing: map[string]bool{"cdown": true},
	}
	draft := importSkills(t, site)
	for index, want := range []string{notASkill, notASkill, skillUnanswered, notASkill} {
		if got := draft.Skills[index]; got.Installed || got.Reason != want || len(got.Files) != 0 || got.Files == nil {
			t.Errorf("skill %d = %+v; want not installed, %q, files []", index, got, want)
		}
	}
}

// A skill holding a file that isn't text, an absolute path, a .. segment
// or a path twice is refused whole, never cleaned up.
func TestABadFileRefusesTheWholeSkill(t *testing.T) {
	cases := map[string]struct {
		content string
		reason  string
	}{
		"a script":            {skillContent("references/a.md", "scripts/run.py"), skillNotText},
		"a json":              {skillContent("data.json"), skillNotText},
		"no extension":        {skillContent("Makefile"), skillNotText},
		"an absolute path":    {skillContent("/etc/a.md"), skillBadPath},
		"a .. segment":        {skillContent("references/../../a.md"), skillBadPath},
		"a lone ..":           {skillContent("../a.md"), skillBadPath},
		"a . segment":         {skillContent("./a.md"), skillBadPath},
		"an empty segment":    {skillContent("a//b.md"), skillBadPath},
		"a backslash":         {skillContent(`a\b.md`), skillBadPath},
		"a hidden character":  {skillContent("a\u200b.md"), skillBadPath},
		"a path twice":        {skillContent("a.md", "a.md"), skillBadPath},
		"a second SKILL.md":   {skillContent("SKILL.md"), skillBadPath},
		"an empty path":       {skillContent(""), skillBadPath},
		"an unclosed header":  {"x\x1fFILE:a.md no end", skillBadPath},
		"a path with a space": {skillContent(" a.md"), skillBadPath},
	}
	for name, test := range cases {
		site := &skillSite{bot: skillBot(linkA), prompts: map[string]prompt{"cmvw320te0001l3089ivvu448": {Type: "SKILL", Slug: "s1", Content: test.content}}}
		got := importSkills(t, site).Skills[0]
		if got.Installed || got.Reason != test.reason || len(got.Files) != 0 || got.Dir != "" {
			t.Errorf("%s: skill = %+v; want not installed, %q, no files", name, got, test.reason)
		}
	}
}

// A skill over the file count or the size cap is refused, never
// truncated; one at the caps is drafted whole.
func TestASkillOverACapIsNotInstalled(t *testing.T) {
	files := func(count int) string {
		paths := make([]string, count-1)
		for index := range paths {
			paths[index] = fmt.Sprintf("r/%d.md", index)
		}
		return skillContent(paths...)
	}
	sized := func(total int) string {
		return strings.Repeat("a", total)
	}
	for name, test := range map[string]struct {
		content   string
		installed bool
	}{
		"24 files":      {files(24), true},
		"25 files":      {files(25), false},
		"128 KiB":       {sized(128 << 10), true},
		"128 KiB and 1": {sized(128<<10 + 1), false},
	} {
		site := &skillSite{bot: skillBot(linkA), prompts: map[string]prompt{"cmvw320te0001l3089ivvu448": {Type: "SKILL", Slug: "s1", Content: test.content}}}
		got := importSkills(t, site).Skills[0]
		if got.Installed != test.installed {
			t.Errorf("%s: installed = %v; want %v (%s)", name, got.Installed, test.installed, got.Reason)
		}
		if !test.installed && (got.Reason != skillTooBig || len(got.Files) != 0) {
			t.Errorf("%s: skill = %q with %d files; want %q and none", name, got.Reason, len(got.Files), skillTooBig)
		}
	}
}

// A bot fetches at most 8 skills, and two skills making one folder keep
// the first.
func TestABotsSkillsAreBounded(t *testing.T) {
	prompts := map[string]prompt{}
	var sources []string
	for index := range 10 {
		id := fmt.Sprintf("cid%d", index)
		prompts[id] = prompt{Type: "SKILL", Slug: fmt.Sprintf("skill-%d", index), Content: "x"}
		sources = append(sources, fmt.Sprintf("https://prompts.chat/prompts/%s_skill-%d", id, index))
	}
	prompts["cid1"] = prompt{Type: "SKILL", Slug: "Skill 0", Content: "x"}
	site := &skillSite{bot: skillBot(sources...), prompts: prompts}
	draft := importSkills(t, site)
	if len(site.asked) != 1+maxBotSkills {
		t.Errorf("fetched %d paths; want the bot and %d skills", len(site.asked), maxBotSkills)
	}
	if draft.Skills[1].Installed || draft.Skills[1].Reason != skillSameDir {
		t.Errorf("a second skill-0 = %+v; want %q", draft.Skills[1], skillSameDir)
	}
	for _, got := range draft.Skills[8:] {
		if got.Installed || got.Reason != skillTooMany {
			t.Errorf("skill past the 8th = %+v; want %q", got, skillTooMany)
		}
	}
}

// CheckSkills holds what a create sends to the draft's rules, since
// create installs it as given.
func TestCheckSkills(t *testing.T) {
	good := Skill{Dir: "http-headers", Files: []SkillFile{{Path: "SKILL.md", Content: "x"}, {Path: "r/a.md", Content: "y"}}}
	if err := CheckSkills([]Skill{good}); err != nil {
		t.Fatalf("a good skill: %v", err)
	}
	if err := CheckSkills(nil); err != nil {
		t.Fatalf("no skills: %v", err)
	}
	many := make([]Skill, maxBotSkills+1)
	for index := range many {
		many[index] = Skill{Dir: fmt.Sprintf("s%d", index), Files: good.Files}
	}
	bad := map[string][]Skill{
		"too many":          many,
		"a bad dir":         {{Dir: "../x", Files: good.Files}},
		"an upper dir":      {{Dir: "X-y", Files: good.Files}},
		"no dir":            {{Files: good.Files}},
		"a dir twice":       {good, good},
		"no files":          {{Dir: "x1"}},
		"no SKILL.md first": {{Dir: "x1", Files: []SkillFile{{Path: "a.md", Content: "x"}}}},
		"an empty SKILL.md": {{Dir: "x1", Files: []SkillFile{{Path: "SKILL.md"}}}},
		"a script":          {{Dir: "x1", Files: []SkillFile{{Path: "SKILL.md", Content: "x"}, {Path: "a.sh", Content: "y"}}}},
		"a .. path":         {{Dir: "x1", Files: []SkillFile{{Path: "SKILL.md", Content: "x"}, {Path: "../a.md", Content: "y"}}}},
		"a hidden char":     {{Dir: "x1", Files: []SkillFile{{Path: "SKILL.md", Content: "x\u202e"}}}},
		"over the cap":      {{Dir: "x1", Files: []SkillFile{{Path: "SKILL.md", Content: strings.Repeat("a", maxSkillBytes+1)}}}},
	}
	for name, skills := range bad {
		if err := CheckSkills(skills); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The fetcher takes a skill's prompt path and no other under /api/prompts.
func TestTheFetcherTakesASkillsPromptPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/prompts/cmvw320te0001l3089ivvu448": true,
		"/api/prompts/CABC":                      false,
		"/api/prompts/cabc/versions":             false,
		"/api/prompts/":                          false,
		"/api/prompts/cabc?x=1":                  false,
	} {
		if got := fetchPath.MatchString(path); got != want {
			t.Errorf("fetchPath(%q) = %v; want %v", path, got, want)
		}
	}
}
