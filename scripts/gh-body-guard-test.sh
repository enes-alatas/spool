#!/usr/bin/env bash
# Fixtures for .claude/hooks/gh-body-guard.sh, the repository's own
# PreToolUse hook (#630). Each case is a Bash call as Claude Code hands it
# to the hook, and the exit the hook must give: 2 refuses, 0 lets it run.
# Names and bodies are synthetic.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
hook="$root/.claude/hooks/gh-body-guard.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

sig='— Fixture · Tester'
printf 'Looks good, @Quinn.\n\n%s\n' "$sig" > "$work/mention.md"
printf 'No sign-off here.\n' > "$work/unsigned.md"
printf 'All fine.\n\n%s\n' "$sig" > "$work/signed.md"
printf 'cc @terra\n%s\n' "$sig" > "$work/field.md"

# json_string escapes a value as a JSON string's contents.
json_string() {
	local s=$1
	s=${s//\\/\\\\}
	s=${s//\"/\\\"}
	s=${s//$'\n'/\\n}
	s=${s//$'\t'/\\t}
	printf '%s' "$s"
}

run_hook() { sh "$hook"; }

fails=0
# check runs the hook, as run_hook runs it, on one Bash call and wants its
# exit and, when given, a phrase of what it says.
check() {
	local want=$1 name=$2 command=$3 description=${4:-run it} phrase=${5:-} got=0 said
	said=$(printf '{"session_id":"s","cwd":"%s","tool_name":"Bash","tool_input":{"description":"%s","command":"%s"}}' \
		"$(json_string "${cwd_dir:-$work}")" "$(json_string "$description")" "$(json_string "$command")" |
		run_hook 2>&1) || got=$?
	if [ "$got" -ne "$want" ] || [[ -n $phrase && $said != *"$phrase"* ]]; then
		echo "FAIL $name: exit $got, want $want ${phrase:+saying \"$phrase\" }${said:+($said)}"
		fails=$((fails + 1))
	else
		echo "ok   $name"
	fi
}

heredoc="cat > /tmp/new.md <<'END'
asking @iris
$sig
END
gh issue comment 3 --body-file /tmp/new.md"

# refused: an @ before a fleet name, in each place a body comes from
check 2 "inline --body" "gh pr comment 12 --body \"over to @terra
$sig\""
check 2 "--body= form" "gh issue comment 3 --body=\"ping @Milo $sig\""
check 2 "-b with a repo flag" "gh -R owner/repo pr review 7 --comment -b \"@enesalatas please look. $sig\""
check 2 "a body file there" "gh issue create --title t --body-file mention.md"
check 2 "a body file written first, by a here-doc" "$heredoc"
check 2 "a body from a variable" "body=\"thanks @oguzenesalatas $sig\"; gh pr comment 1 --body \"\$body\""
check 2 "gh api, a raw field" "gh api repos/o/r/issues/1/comments -f body=\"cc @quinn $sig\""
check 2 "gh api, a field from a file" "gh api repos/o/r/issues/1/comments -F body=@field.md"
check 2 "the mention at the start" "gh pr comment 1 --body \"@elliot see above $sig\""
check 2 "close with a comment" "gh issue close 5 --comment \"fixed, thanks @terra $sig\""
check 2 "reopen with a comment" "gh issue reopen 5 --comment \"@milo again $sig\""
check 2 "a mention in parentheses" "gh pr comment 1 --body \"(cc @terra) $sig\""

# refused: a body with no signature line
check 2 "inline, unsigned" "gh pr comment 12 --body \"Looks good.\""
check 2 "a body file, unsigned" "gh pr create --title t --body-file unsigned.md"
check 2 "gh api comment, unsigned" "gh api repos/o/r/issues/1/comments -f body=\"done\""
check 2 "close -c, unsigned" "gh pr close 5 -c \"superseded by 6\""

# refused, saying so: a body only the call itself resolves
check 2 "a body file named by a variable" "F=signed.md; gh pr comment 5 --body-file \"\$F\"" "" "can't read"
check 2 "a typed field from a variable's file" "gh api repos/o/r/issues/1/comments -F body=@\$F" "" "can't read"
check 2 "a body from a command substitution" "gh pr comment 5 --body \"\$(cat signed.md)\"" "" "can't read"

# let through
check 0 "a name with no @" "gh pr comment 12 --body \"over to terra
$sig\""
check 0 "a signed body file" "gh pr create --title t --body-file signed.md"
check 0 "an address, not a mention" "gh issue comment 3 --body \"write to ops@terra.example $sig\""
check 0 "a longer word" "gh pr comment 1 --body \"see @terraform docs $sig\""
check 0 "no body: a label edit" "gh pr edit 12 --add-label queue"
check 0 "no body: an approval" "gh pr review 12 --approve"
check 0 "not a body: a search" "gh issue list --search \"@terra\""
check 0 "not gh" "git commit -m \"note for @terra\""
check 0 "a read" "gh pr view 12 --json body"
check 0 "the description is not the body" "gh pr view 12" "ask @terra"
check 0 "gh api, a read" "gh api repos/o/r/pulls/1/comments --jq '.[].body'"
check 0 "a word ending in gh" "sigh issue comment 3 --body \"@terra\""
check 0 "gh api, a read filtering on a name" "gh api repos/o/r/issues/1/comments --jq '.[] | select(.body | test(\"@terra\"))'"
check 0 "no body: a title edit" "gh pr edit 12 --title \"fix: @terra's note\""
check 0 "close with no comment" "gh issue close 5"
check 0 "close with a signed comment" "gh pr close 5 -c \"superseded by 6 $sig\""

# A body file named from home (#1194): the shell expands an unquoted ~/,
# so the hook reads it there; quoted, or after --body-file=, it stays as
# written, and a file that isn't there is named as such.
run_hook() { HOME="$work" sh "$hook"; }
check 0 "a signed body file under ~/" "gh pr create --title t --body-file ~/signed.md"
check 2 "a body file under ~/ with a mention" "gh issue comment 3 --body-file ~/mention.md" "" "@-mentions Quinn"
check 2 "a body file under ~/, unsigned" "gh pr create --title t --body-file ~/unsigned.md" "" "no signature"
check 2 "a quoted ~/ body file" "gh pr create --title t --body-file \"~/signed.md\"" "" "isn't there"
check 2 "a ~/ body file after =" "gh pr create --title t --body-file=~/signed.md" "" "isn't there"
check 2 "a body file not there yet" "gh pr create --title t --body-file ~/later.md" "" "isn't there"
run_hook() { sh "$hook"; }

if printf '{"tool_name":"Read","tool_input":{"file_path":"/x"}}' | sh "$hook"; then
	echo "ok   a tool with no command"
else
	echo "FAIL a tool with no command"
	fails=$((fails + 1))
fi

# A loop's user settings run the hook from a clone's ref, not a checkout's
# .claude (CONVENTIONS.md): the same calls, through that command.
clone="$work/clone"
git init -q "$clone"
mkdir -p "$clone/.claude/hooks"
cp "$root"/.claude/hooks/* "$clone/.claude/hooks/"
git -C "$clone" add -A
git -C "$clone" -c user.name=fixture -c user.email=fixture@example.invalid commit -qm hooks
run_hook() { sh -c "$(git -C "$clone" show HEAD:.claude/hooks/gh-body-guard.sh)" gh-body-guard "$clone" HEAD; }
check 2 "from a clone: a mention" "gh pr comment 12 --body \"over to @terra $sig\"" "" "@-mentions terra"
check 2 "from a clone: unsigned" "gh pr comment 12 --body \"Looks good.\"" "" "no signature"
check 0 "from a clone: signed" "gh pr create --title t --body-file signed.md"
run_hook() { sh -c "$(git -C "$clone" show HEAD:.claude/hooks/gh-body-guard.sh)" gh-body-guard "$clone" no-such-ref; }
check 0 "from a clone: a ref without its files" "gh pr comment 12 --body \"Looks good.\""

# pr-guards' rules before the call (#1371). The hook clone carries CI's two
# scripts, and a working clone of the same repository (by origin) holds a
# base on origin/main and a branch on top of it.
url=https://example.invalid/fixture/spool.git
mkdir -p "$clone/scripts"
cp "$root/scripts/commit-subjects.sh" "$root/scripts/other-half.sh" "$root/scripts/issue-headings.sh" "$clone/scripts/"
mkdir -p "$clone/.github/ISSUE_TEMPLATE"
cp "$root"/.github/ISSUE_TEMPLATE/*.yml "$clone/.github/ISSUE_TEMPLATE/"
git -C "$clone" add scripts .github
git -C "$clone" -c user.name=fixture -c user.email=fixture@example.invalid commit -qm scripts
git -C "$clone" remote add origin "$url"
wt="$work/wt"
git init -q "$wt"
gitw() { git -C "$wt" -c user.name=fixture -c user.email=fixture@example.invalid "$@"; }
gitw remote add origin "$url"
gitw commit -q --allow-empty -m "chore: the base"
gitw update-ref refs/remotes/origin/main HEAD
mkdir -p "$wt/internal"
echo x > "$wt/internal/x.go"
gitw add internal/x.go
gitw commit -q -m "feat(api): a hub-only change"
printf 'Closes #1\n\n%s\n' "$sig" > "$wt/nohalf.md"
printf 'Closes #1\nOther half: #12\n\n%s\n' "$sig" > "$wt/half.md"
printf 'Closes #1\nOther half: \n\n%s\n' "$sig" > "$wt/emptyhalf.md"
run_hook() { sh -c "$(git -C "$clone" show HEAD:.claude/hooks/gh-body-guard.sh)" gh-body-guard "$clone" HEAD; }
cwd_dir=$wt
check 0 "push: good commits" "git push -u origin feat/x"
check 2 "pr create: a feat with no other half" "gh pr create --title \"feat(api): x\" --body-file nohalf.md" "" "Other half:"
check 2 "pr create: an empty other half" "gh pr create -t 'feat: x' --body-file emptyhalf.md" "" "is empty"
check 0 "pr create: the other half named" "gh pr create --title \"feat(api): x\" --body-file half.md"
check 0 "pr create: not a feat" "gh pr create --title \"fix(api): x\" --body-file nohalf.md"
check 0 "pr edit: no title, nothing to judge" "gh pr edit --body-file nohalf.md"
check 2 "pr edit: HEAD's PR, a feat with no other half" "gh pr edit --title \"feat(api): x\" --body-file nohalf.md" "" "Other half:"
check 0 "pr edit: a PR named by number is some other branch's" "gh pr edit 5 --title \"feat(api): x\" --body-file nohalf.md"
check 0 "pr edit: a PR named by branch" "gh pr edit feat/other -t 'feat: x' --body-file nohalf.md"
mkdir -p "$wt/web"
echo x > "$wt/web/x.ts"
gitw add web/x.ts
gitw commit -q -m "feat(web): the room half"
check 0 "pr create: both halves" "gh pr create --title \"feat: x\" --body-file nohalf.md"
# An issue filed with gh carries its form's headings (#1474).
printf '### What needs doing\nx\n### Where it lives\ny\n### Out of scope\nz\n\n%s\n' "$sig" > "$wt/chore.md"
printf '### Observed behavior\nx\n### Expected behavior\ny\n### Where it lives\nz\n\n%s\n' "$sig" > "$wt/bug.md"
printf 'No headings.\n\n%s\n' "$sig" > "$wt/plain.md"
check 0 "issue create: a chore with its headings" "gh issue create --title t --label chore --body-file chore.md"
check 2 "issue create: a chore without them" "gh issue create --title t --label chore --body-file plain.md" "" "### What needs doing"
check 2 "issue create: a bug with no severity" "gh issue create -t t -l bug --body-file bug.md" "" "severity:"
check 0 "issue create: a bug with severity, comma-joined" "gh issue create -t t -l bug,severity:low --body-file bug.md"
check 0 "issue create: a bug with severity, two flags, -R" "gh -R o/r issue create -t t --label=bug --label \"severity:high\" --body-file bug.md"
check 2 "issue create: a quoted type label" "gh issue create -t t --label \"new feature\" --body-file chore.md" "" "### Problem statement"
check 2 "issue create: an inline body" "gh issue create -t t -l chore --body \"no headings $sig\"" "" "### Where it lives"
check 0 "issue create: untyped is CI's" "gh issue create -t t --body-file plain.md"
check 0 "issue create: community is exempt" "gh issue create -t t -l community --body-file plain.md"
check 0 "issue create: --web" "gh issue create --web -l chore"
check 0 "issue comment: not a filing" "gh issue comment 3 --body-file plain.md"
gitw commit -q --allow-empty -m "the fleet channel is a tab"
check 2 "push: a bad subject" "git push" "" "pr-guards will refuse"
check 2 "push: piped, with an upstream flag" "git push -u origin HEAD 2>&1 | tail -n 5" "" "the subject isn't"
check 2 "push then a gh call" "git push && gh pr comment 3 --body-file half.md" "" "pr-guards will refuse"
check 2 "push: through gh's credential helper" "git -c credential.helper='!gh auth git-credential' push -u origin HEAD" "" "pr-guards will refuse"
check 2 "push: two -c settings" "git -c a.b=1 -c credential.helper=\"!gh auth git-credential\" push" "" "pr-guards will refuse"
check 2 "push: -C into the clone" "cd / && git -C $wt push origin HEAD" "" "pr-guards will refuse"
check 0 "push: -C into a directory that isn't there" "git -C $work/nowhere push"
check 0 "push: -C into another directory" "git -C $work push"
check 0 "push: a deletion" "git push origin :old-branch"
check 0 "push: --delete" "git push origin --delete old-branch"
check 0 "push: a range the clone can't resolve" "git push origin no-such-branch"
# A loop's own branch is never a PR, so its ledger isn't checked (#1390).
check 0 "push: HEAD to a loop/ branch" "git push origin HEAD:loop/milo"
check 0 "push: to refs/heads/loop/" "git -c a.b=1 push origin HEAD:refs/heads/loop/milo"
check 2 "push: loop/ further into the destination is still checked" "git push origin HEAD:feat/loop/x" "" "pr-guards will refuse"
gitw switch -q -c loop/milo
check 0 "push: the loop's branch checked out" "git push -u origin HEAD"
check 0 "push: the loop's branch by name" "git push origin loop/milo"
check 2 "push: the loop's branch to a PR branch is checked" "git push origin loop/milo:feat/y" "" "pr-guards will refuse"
gitw switch -q -
gitw remote set-url origin https://example.invalid/someone/else.git
check 0 "push: another project's clone" "git push"
cwd_dir=

[ "$fails" -eq 0 ] || { echo "$fails case(s) failed"; exit 1; }
echo "all cases pass"
