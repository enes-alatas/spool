package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func loadShapes(t *testing.T) []Shape {
	t.Helper()
	shapes, err := LoadShapes("../../scripts/secret-rules.awk")
	if err != nil {
		t.Fatal(err)
	}
	return shapes
}

func TestShapesComeFromTheSharedRules(t *testing.T) {
	shapes := loadShapes(t)
	byName := map[string]Shape{}
	for _, shape := range shapes {
		byName[shape.Name] = shape
	}
	for _, name := range []string{"telegram-bot-token", "anthropic-key", "github-token", "assigned-secret", "loop-bot-handle"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("shape %s not loaded; got %d shapes", name, len(shapes))
		}
	}
	if !byName["loop-bot-handle"].BodyOnly || byName["github-token"].BodyOnly {
		t.Error("body-only scope not carried over from only[i]")
	}
	// assigned-secret's pattern holds an escaped quote in its awk string.
	if !byName["assigned-secret"].Re.MatchString(`token = "abcdefghij_fixture_0123456789"`) {
		t.Error("assigned-secret does not match a quoted assignment")
	}
}

// fakeResolver resolves every link except the ones it is told are dead.
type fakeResolver map[string]bool

func (dead fakeResolver) Resolves(url string) (bool, error) { return !dead[url], nil }

// TestSyntheticSlipsAreCaught holds the graders to the slips they exist
// for: each synthetic case fails exactly the grader named here, and the
// clean one and the judge-only one fail none.
func TestSyntheticSlipsAreCaught(t *testing.T) {
	want := map[string]string{
		"synthetic-guessed-anchor":            "links_resolve",
		"synthetic-closing-keyword-in-commit": "commit_no_closing_keyword",
		"synthetic-decision-reasked":          "",
		"synthetic-missing-trailer":           "trailer",
		"synthetic-mention-on-github":         "github_no_mention",
		"synthetic-unsigned-comment":          "github_signature",
		"synthetic-unshown-ref":               "refs_shown",
		"synthetic-group-reaches-nobody":      "group_reaches_someone",
		"synthetic-clean":                     "",
	}
	paths, err := filepath.Glob("../../eval/synthetic/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no synthetic cases: %v", err)
	}
	var cases []Case
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var turnCase Case
		if err := json.Unmarshal(raw, &turnCase); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		cases = append(cases, turnCase)
	}

	resolver := fakeResolver{"https://github.com/enes-alatas/spool/issues/1#issuecomment-1": true}
	report := Run(cases, Graders(loadShapes(t), nil, resolver))
	failed := map[string][]string{}
	for _, failure := range report.Failures {
		failed[failure.CaseID] = append(failed[failure.CaseID], failure.Grader)
	}
	for _, turnCase := range cases {
		grader, known := want[turnCase.ID]
		if !known {
			t.Errorf("%s has no expectation here; add one", turnCase.ID)
			continue
		}
		got := failed[turnCase.ID]
		switch {
		case grader == "" && len(got) > 0:
			t.Errorf("%s failed %v, want none", turnCase.ID, got)
		case grader != "" && !slices.Equal(got, []string{grader}):
			t.Errorf("%s failed %v, want exactly [%s]", turnCase.ID, got, grader)
		}
	}
}

func TestACredentialShapeFailsWhereverItIsWritten(t *testing.T) {
	// Built here rather than in eval/synthetic: the PR secret scan keeps
	// credential shapes in test files.
	token := "12345678:" + "FAKE_fixture_value_not_a_real_token_x"
	shapes := loadShapes(t)
	for name, turnCase := range map[string]Case{
		"a group send":  {Sends: []Send{{Destination: "group", Text: "it is " + token}}},
		"a command":     {GitHubWrites: []GitHubWrite{{Command: "curl -H 'x: " + token + "' https://example.com"}}},
		"a final reply": {FinalText: "set to " + token},
		"an export":     {FinalText: "set to <redacted:tg_bot_token>"},
	} {
		if verdict := gradeNoCredential(shapes, turnCase); verdict.Pass {
			t.Errorf("%s: a credential passed", name)
		}
	}
	// A fleet identifier is a quote of the group: fine in chat, not on
	// GitHub.
	handle := "@terra_spool_bot"
	if verdict := gradeNoCredential(shapes, Case{Sends: []Send{{Destination: "group", Text: handle}}}); !verdict.Pass {
		t.Errorf("a bot handle in chat failed: %s", verdict.Detail)
	}
	github := Case{GitHubWrites: []GitHubWrite{{Command: `gh issue comment 1 --body "ask ` + handle + `"`}}}
	if verdict := gradeNoCredential(shapes, github); verdict.Pass {
		t.Error("a bot handle on GitHub passed")
	}
}

func TestArtifactsReadWhatIsPublished(t *testing.T) {
	cases := []struct {
		name     string
		write    GitHubWrite
		kind     string
		text     string
		authored bool
	}{
		{
			name:     "commit message from a heredoc",
			write:    GitHubWrite{Command: "git commit -q -m \"$(cat <<'EOF'\nfix: x\n\n— Terra · Backend Engineer\nEOF\n)\""},
			kind:     "commit",
			text:     "fix: x\n\n— Terra · Backend Engineer",
			authored: true,
		},
		{
			name:     "an amend that keeps its message",
			write:    GitHubWrite{Command: "git commit --amend --no-edit"},
			kind:     "commit",
			authored: false,
		},
		{
			name:     "a review body written to a file first",
			write:    GitHubWrite{Command: "f=$(mktemp) && cat > $f <<'EOF'\nLooks right.\n\n— Quinn · Quality Reviewer\nEOF\ngh pr review 9 --comment --body-file $f"},
			kind:     "pr review",
			text:     "Looks right.\n\n— Quinn · Quality Reviewer",
			authored: true,
		},
		{
			name:     "a body file the turn wrote with its Write tool",
			write:    GitHubWrite{Command: "gh issue create --title T --body-file /w/body.md", Files: map[string]string{"/w/body.md": "B"}},
			kind:     "issue create",
			text:     "T\nB",
			authored: true,
		},
		{
			name:     "an escaped quote inside a double-quoted body",
			write:    GitHubWrite{Command: `gh issue comment 3 --body "say \"hi\" \n ok"`},
			kind:     "issue comment",
			text:     `say "hi" \n ok`,
			authored: true,
		},
		{
			name:     "an edit is not authored",
			write:    GitHubWrite{Command: "gh issue edit 3 --body-file -  <<'EOF'\nnew\nEOF"},
			kind:     "issue edit",
			text:     "new",
			authored: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Artifacts(tc.write)
			if len(got) != 1 {
				t.Fatalf("got %d artifacts, want 1: %+v", len(got), got)
			}
			if got[0].Kind != tc.kind || got[0].Text != tc.text || got[0].Authored != tc.authored {
				t.Errorf("got %+v, want kind %q text %q authored %v", got[0], tc.kind, tc.text, tc.authored)
			}
		})
	}
}

func TestACommandSplitsAtEachWrite(t *testing.T) {
	// The PR body's closing keyword belongs to the PR, not to the commit
	// the same command made first.
	write := GitHubWrite{Command: `git commit -m "feat: y" && git push && gh pr create --title "feat: y" --body "Closes #4"`}
	turnCase := Case{GitHubWrites: []GitHubWrite{write}}
	if verdict := gradeCommitNoClosingKeyword(turnCase); !verdict.Pass {
		t.Errorf("commit graded on the PR body: %s", verdict.Detail)
	}
}

func TestMentionsSkipEmailsFilesAndScopes(t *testing.T) {
	text := "Co-Authored-By: Claude <noreply@anthropic.com>\n-f body=@- and @types/node, but @quinn and (@milo)"
	if got := mentions(text); !slices.Equal(got, []string{"quinn", "milo"}) {
		t.Errorf("mentions = %v, want [quinn milo]", got)
	}
}

func TestTrailerRules(t *testing.T) {
	selfPaced := Case{Pacing: store.PacingSelf, MinWakeSec: 300, MaxWakeSec: 14400}
	for _, tc := range []struct {
		name    string
		mutate  func(*Case)
		applies bool
		pass    bool
	}{
		{"in range", func(c *Case) { c.FinalText = "done\n[next-wake: 45m]" }, true, true},
		{"too long", func(c *Case) { c.FinalText = "done\n[next-wake: 6h]" }, true, false},
		{"missing on a self-paced loop", func(c *Case) { c.FinalText = "done" }, true, false},
		{"missing on a fixed loop", func(c *Case) { c.FinalText, c.Pacing = "done", store.PacingFixed }, false, false},
		{"none on a rotation handoff", func(c *Case) { c.Trigger, c.FinalText = store.TriggerRotation, "handoff" }, true, true},
		{"one on a rotation handoff", func(c *Case) { c.Trigger, c.FinalText = store.TriggerRotation, "handoff\n[next-wake: 1h]" }, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turnCase := selfPaced
			tc.mutate(&turnCase)
			verdict := gradeTrailer(turnCase)
			if verdict.Applies != tc.applies || verdict.Pass != tc.pass {
				t.Errorf("got %+v, want applies %v pass %v", verdict, tc.applies, tc.pass)
			}
		})
	}
}

func TestSignatureFollowsTheMission(t *testing.T) {
	// Quinn's mission signs reviews, not its loop-branch commits.
	turnCase := Case{Loop: "quinn", GitHubWrites: []GitHubWrite{{Command: `git commit --allow-empty -m "chore(quinn): approve #9"`}}}
	signing := Signing{"quinn": {"pr review"}}
	if verdict := gradeGitHubSignature(signing, turnCase); verdict.Applies {
		t.Errorf("an unscoped commit was judged: %+v", verdict)
	}
	turnCase.Loop = "terra"
	if verdict := gradeGitHubSignature(signing, turnCase); verdict.Pass {
		t.Error("a loop the table does not name should sign every authored artifact")
	}
}

// event builds one stored event.
func event(turn, kind, payload string) *store.Event {
	return &store.Event{SessionID: "s1", TurnID: turn, Type: kind, Payload: payload}
}

func TestExtractBuildsCasesFromEvents(t *testing.T) {
	events := []*store.Event{
		// t1: a group turn that sends twice, the second replying to the
		// first's own ref, and writes to GitHub.
		event("t1", "envelope", `{"trigger":"message","conversation":"group","text":"[message from @milo · group · ref:5]\n\ngo"}`),
		event("t1", "assistant", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"u1","name":"mcp__spool__send_message","input":{"destination":"group","text":"on it","reply_to":"ref:5"}}]}}`),
		event("t1", "user", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"u1","content":"{\"message_id\":6,\"ref\":\"ref:6\"}"}]}}`),
		event("t1", "assistant", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"u2","name":"mcp__spool__send_message","input":{"destination":"owner_dm","text":"private words"}},{"type":"tool_use","id":"u3","name":"Bash","input":{"command":"gh issue comment 1 --body \"SECRETVALUE here\""}},{"type":"tool_use","id":"u4","name":"Bash","input":{"command":"gh pr view 1"}}]}}`),
		event("t1", "result", `{"type":"result","result":"done\n[next-wake: 45m]"}`),
		// t2: started by an owner DM — left out whole.
		event("t2", "envelope", `{"trigger":"message","conversation":"owner_dm","text":"[message from @enes · owner_dm · ref:7]\n\nhi"}`),
		event("t2", "result", `{"type":"result","result":"ok"}`),
		// t3: a tick that never finished — left out.
		event("t3", "envelope", `{"trigger":"tick","text":"[tick]"}`),
	}
	clean := func(text string) string { return strings.ReplaceAll(text, "SECRETVALUE", "<redacted:x>") }
	cases := Extract(events, LoopInfo{Name: "terra", Pacing: store.PacingSelf}, clean)
	if len(cases) != 1 || cases[0].ID != "t1" {
		t.Fatalf("got %d cases (%v), want t1 alone", len(cases), cases)
	}
	turnCase := cases[0]
	if !slices.Equal(turnCase.ShownRefs, []string{"ref:5"}) {
		t.Errorf("shown refs = %v, want [ref:5]", turnCase.ShownRefs)
	}
	if len(turnCase.Sends) != 2 || turnCase.Sends[0].Ref != "ref:6" {
		t.Fatalf("sends = %+v, want two with the first's ref recorded", turnCase.Sends)
	}
	if dm := turnCase.Sends[1]; !dm.Withheld || dm.Text != "" {
		t.Errorf("owner DM send kept its text: %+v", dm)
	}
	if len(turnCase.GitHubWrites) != 1 || !strings.Contains(turnCase.GitHubWrites[0].Command, "<redacted:x>") {
		t.Errorf("github writes = %+v, want the one write, cleaned", turnCase.GitHubWrites)
	}
	if turnCase.Pacing != store.PacingSelf || turnCase.FinalText != "done\n[next-wake: 45m]" {
		t.Errorf("case = %+v", turnCase)
	}
}
