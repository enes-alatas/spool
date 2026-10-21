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
		"$(json_string "$work")" "$(json_string "$description")" "$(json_string "$command")" |
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

[ "$fails" -eq 0 ] || { echo "$fails case(s) failed"; exit 1; }
echo "all cases pass"
