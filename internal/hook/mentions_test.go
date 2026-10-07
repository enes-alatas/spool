package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fleet is a synthetic fleet's names as the hub hands them over: loops,
// their bots, and the people the fleet knows.
var fleet = Rules{Mentions: []string{"alpha", "bravo", "alpha_fixture_bot", "Fixture-Person"}}

// TestMentionsInGHBodiesAreRefused: a gh body that @-mentions a fleet name,
// written the way a loop writes it and the next obvious ways after that
// (#413).
func TestMentionsInGHBodiesAreRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte("Ready for review, @bravo.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`gh pr comment 12 --body "ready @bravo"`,
		`gh pr comment 12 -b "ready @bravo"`,
		`gh pr comment 12 --body="ready @bravo"`,
		`gh pr comment 12 -b"ready @bravo"`,
		`gh issue comment 12 --body "@alpha taking this"`,
		`gh issue create --title t --body "cc @Bravo"`,
		`gh issue edit 12 --body "(@bravo)"`,
		`gh pr create --title t --body "thanks,@bravo"`,
		`gh pr edit 12 --body "@bravo: done"`,
		`gh pr review 12 --approve --body "LGTM @alpha."`,
		`gh -R fixture/repo pr comment 12 --body "@bravo"`,
		`gh pr comment 12 --repo fixture/repo --body "@bravo"`,
		`gh issue comment 12 --body "ping @fixture-person"`,
		// a bot's name pings the login GitHub reads before its underscore
		`gh issue comment 12 --body "@alpha_fixture_bot"`,
		// a body read from a here-document, a pipe or a here-string
		"gh pr comment 12 --body-file - <<'EOF'\nReady, @bravo.\nEOF",
		"gh pr comment 12 -F - <<EOF\nReady, @bravo.\nEOF",
		`echo "@bravo ready" | gh pr comment 12 --body-file -`,
		`gh pr comment 12 --body-file - <<< "@bravo ready"`,
		// a body read from a file, there already or written by the same script
		`gh pr comment 12 --body-file body.md`,
		`gh pr comment 12 --body-file=body.md`,
		"cat > new.md <<'EOF'\nping @alpha\nEOF\ngh pr comment 12 --body-file new.md",
		// a body held in a shell variable the script set before
		"body=$(cat <<'EOF'\nhi @bravo\nEOF\n)\ngh pr comment 12 --body \"$body\"",
		`B='hi @bravo'; gh pr comment 12 --body "$B"`,
		`B='hi @bravo'; gh pr comment 12 --body="$B"`,
		`B='hi @bravo'; gh api repos/fixture/repo/issues/12/comments -f body="$B"`,
		// a body a command substitution reads from a file already there
		`gh pr comment 12 --body "$(cat body.md)"`,
		"gh pr comment 12 --body \"`cat body.md`\"",
		`gh pr comment 12 --body "$(< body.md)"`,
		// gh api's fields and input
		`gh api repos/fixture/repo/issues/12/comments -f body="@bravo ready"`,
		`gh api repos/fixture/repo/issues/12/comments --raw-field "body=@bravo ready"`,
		`gh api repos/fixture/repo/issues/12/comments -F body=@body.md`,
		`gh api repos/fixture/repo/issues/12/comments --input body.md`,
		`gh api graphql -f query='mutation { addComment(input: {body: "@bravo"}) { clientMutationId } }'`,
		// wrapped the ways the shared-state rules see through
		`cd ../wt && gh pr comment 12 --body "@bravo"`,
		`timeout 30 gh pr comment 12 --body "@bravo"`,
		`bash -c 'gh pr comment 12 --body "@bravo"'`,
	} {
		call := bash(command)
		call.Cwd = dir
		reason := Check(call, fleet)
		if reason == "" {
			t.Errorf("%q was let through", command)
			continue
		}
		if !strings.Contains(reason, "without the @") {
			t.Errorf("%q refused with %q, which doesn't say what to do", command, reason)
		}
	}
}

// TestMentionLikeTextIsLetThrough: a body that names the fleet without
// pinging anyone, a mention of a name the fleet doesn't hold, and a gh
// command that sends no body all run, and so does everything when the hub
// hands the hook no names.
func TestMentionLikeTextIsLetThrough(t *testing.T) {
	for _, command := range []string{
		`gh pr comment 12 --body "ready, bravo"`,
		`gh pr comment 12 --body "mail alpha@fixture.example"`,
		`gh pr comment 12 --body "see @alphabet and @bravos"`,
		`gh pr comment 12 --body "written as ` + "`@bravo`" + ` in the prompt"`,
		"gh pr comment 12 --body-file - <<'EOF'\n```\n@bravo\n```\nEOF",
		`gh pr comment 12 --body "@octocat"`,
		`gh pr view 12 --comments`,
		`gh pr list --search "@bravo"`,
		`gh issue list --assignee alpha`,
		`gh pr comment 12 --body-file missing.md`,
		`B='ready, bravo'; gh pr comment 12 --body "$B"`,
		`gh pr comment 12 --body "$(cat missing.md)"`,
		`git commit -m "thanks @bravo"`,
		`echo "@bravo"`,
	} {
		if reason := Check(bash(command), fleet); reason != "" {
			t.Errorf("%q refused: %s", command, reason)
		}
	}
	if reason := Check(bash(`gh pr comment 12 --body "@bravo"`), Rules{}); reason != "" {
		t.Errorf("with no names, a mention was refused: %s", reason)
	}
}
