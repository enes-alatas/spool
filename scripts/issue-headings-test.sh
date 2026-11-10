#!/usr/bin/env bash
# Self-test for scripts/issue-headings.sh and the typing step that runs it,
# scripts/issue-type-flag.sh (#1474). The headings come from the repo's own
# forms, so a form edit is tested here too. Run under `bash -e` with a stub
# `gh`, the way Actions runs a `run:` block.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
fails=0
forms=.github/ISSUE_TEMPLATE
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# check: want-exit want-output label labels-csv body
check() {
	local got=0 out
	printf '%s\n' "$4" | tr ',' '\n' | grep . > "$work/labels" || : > "$work/labels"
	out=$(printf '%s' "$5" | bash scripts/issue-headings.sh "$forms" "$work/labels" | paste -sd'|' -) || got=$?
	if [ "$got" -ne "$1" ] || [ "$out" != "$2" ]; then
		echo "FAIL $3: exit $got, \"$out\"; want $1, \"$2\""
		fails=$((fails + 1))
	else
		echo "ok   $3"
	fi
}

nl=$'\n'
bug="### Observed behavior${nl}x${nl}### Expected behavior${nl}y${nl}### Where it lives${nl}z"
chore="### What needs doing${nl}x${nl}### Where it lives${nl}y${nl}### Out of scope${nl}z"
feat="### Problem statement${nl}a${nl}### Coupling points${nl}b${nl}### Seams / ADRs touched${nl}c${nl}### Proposed shape${nl}d${nl}### Acceptance scenarios${nl}e${nl}### Out of scope${nl}f"

check 0 "" "a bug with its headings and severity" "bug,severity:low" "$bug"
check 1 "label: severity:* (critical, high, medium, low or info)" "a bug with no severity" "bug" "$bug"
check 1 "heading: ### Observed behavior|heading: ### Expected behavior|heading: ### Where it lives|label: severity:* (critical, high, medium, low or info)" \
	"a bug with only a label, as #1468 was" "bug" "The paused loop answers."
check 0 "" "a chore with its headings" "chore" "$chore"
check 1 "heading: ### Out of scope" "headings out of order" "chore" \
	"### What needs doing${nl}x${nl}### Out of scope${nl}z${nl}### Where it lives${nl}y"
check 0 "" "another heading between them" "chore" \
	"### What needs doing${nl}x${nl}### A note${nl}n${nl}### Where it lives${nl}y${nl}### Out of scope${nl}z"
check 0 "" "trailing hashes, spaces and CRLF" "chore" \
	"### What needs doing ###${nl}x${nl}###   Where it lives  "$'\r'"${nl}y${nl}### Out of scope"
check 1 "heading: ### Where it lives|heading: ### Out of scope" "a heading inside a code fence is not one" "chore" \
	"### What needs doing${nl}\`\`\`${nl}### Where it lives${nl}### Out of scope${nl}\`\`\`"
check 1 "heading: ### What needs doing" "a ## heading is not ###" "chore" \
	"## What needs doing${nl}x${nl}### Where it lives${nl}y${nl}### Out of scope"
check 0 "" "a feature with its required headings, Open questions left out" "new feature" "$feat"
check 1 "heading: ### Acceptance scenarios" "a feature with no scenarios heading" "new feature" \
	"${feat/\#\#\# Acceptance scenarios${nl}e${nl}/}"
check 0 "" "an optional improvement heading may be left out" "improvement" \
	"### Current behavior and friction${nl}a${nl}### Coupling points${nl}b${nl}### Seams / ADRs touched${nl}c${nl}### Proposed shape${nl}d${nl}### Out of scope"
check 1 "heading: ### Behavior contract" "a refactor's checkbox field is a heading, its option is not" "refactor" \
	"### Current shape${nl}a${nl}### Target shape${nl}b${nl}### Seams / ADRs touched${nl}c${nl}- [x] Caller-visible behavior is unchanged by this refactor.${nl}### Out of scope"
check 0 "" "a community issue is exempt" "community,triage" "anything"
check 0 "" "untyped is the typing step's" "" "anything"
check 0 "" "two types are the typing step's" "bug,chore" "anything"
check 1 "heading: ### Observed behavior|heading: ### Expected behavior|heading: ### Where it lives|label: severity:* (critical, high, medium, low or info)" \
	"triage beside a type picks no second form" "bug,triage" "No headings."
check 0 "" "a security issue's Severity dropdown is not a heading" "security" \
	"### Affected surface${nl}a${nl}### Impact and exploit path${nl}b${nl}### Proposed hardening${nl}c${nl}### Out of scope${nl}d"
check 0 "" "every form's own headings pass it" "question" "### Question${nl}q"

# A form with a quoted label, in a scratch forms dir.
mkdir -p "$work/forms"
cat > "$work/forms/odd.yml" <<'YML'
name: Odd
labels: ['question']
body:
  - type: markdown
    attributes:
      value: hello
  - type: textarea
    attributes:
      label: "Quoted label"
    validations:
      required: true
  - type: input
    attributes:
      label: Not required
YML
printf 'question\n' > "$work/labels"
out=$(printf '### Not required\n' | bash scripts/issue-headings.sh "$work/forms" "$work/labels")
if [ "$out" = "heading: ### Quoted label" ]; then echo "ok   a quoted label, a markdown block, an optional input"; else
	echo "FAIL quoted label: \"$out\""
	fails=$((fails + 1))
fi
if bash scripts/issue-headings.sh /nonexistent "$work/labels" < /dev/null 2>/dev/null; [ $? -eq 2 ]; then
	echo "ok   a usage error exits 2"
else
	echo "FAIL a usage error exits 2"
	fails=$((fails + 1))
fi

# The typing step, with a stub gh.
stub=$(mktemp -d)
trap 'rm -rf "$work" "$stub"' EXIT
cat > "$stub/gh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$CALLS"
case "$*" in
api\ repos/o/r/issues/7/comments*) [ "$(cat "$COMMENTS")" = FAIL ] && exit 1; cat "$COMMENTS" ;;
api\ repos/*) cat "$FETCH" ;;
*--body-file*) cat "${@: -1}" > "$NOTE" ;;
esac
STUB
chmod +x "$stub/gh"

step() { # label issue-json told-before(0/1) want-exit want-writes
	local got=0 calls
	printf '%s' "$2" > "$stub/fetch.json"
	case $3 in
	1) echo '<!-- spool:needs-headings -->' > "$stub/comments" ;;
	fail) echo FAIL > "$stub/comments" ;;
	*) : > "$stub/comments" ;;
	esac
	: > "$stub/calls"
	: > "$stub/note"
	PATH="$stub:$PATH" CALLS="$stub/calls" FETCH="$stub/fetch.json" COMMENTS="$stub/comments" NOTE="$stub/note" \
		GITHUB_REPOSITORY=o/r NUM=7 bash -e scripts/issue-type-flag.sh >/dev/null 2>&1 || got=$?
	calls=$(grep -v '^api ' "$stub/calls" | sed 's/--body-file .*/--body-file/' | paste -sd'|' - | sed 's/|/ | /g')
	if [ "$got" -ne "$4" ] || [ "$calls" != "$5" ]; then
		echo "FAIL $1: exit $got, writes \"$calls\"; want $4, \"$5\""
		fails=$((fails + 1))
	else
		echo "ok   $1"
	fi
}
issue() { # created body labels...
	local c=$1 b=$2
	shift 2
	jq -nc --arg c "$c" --arg b "$b" '{created_at: $c, body: $b, labels: ($ARGS.positional | map({name: .}))}' --args "$@"
}
new=2026-11-11T09:00:00Z
old=2026-11-10T14:59:59Z
add="issue edit 7 -R o/r --add-label needs-type"
tell="$add | issue comment 7 -R o/r --body-file"
remove="issue edit 7 -R o/r --remove-label needs-type"

step "untyped is flagged" "$(issue $new "$chore")" 0 0 "$add"
step "untyped, flagged already, nothing" "$(issue $new "$chore" needs-type)" 0 0 ""
step "a community report is exempt and cleared" "$(issue $new "x" community triage needs-type)" 0 0 "$remove"
step "typed with headings, nothing" "$(issue $new "$chore" chore)" 0 0 ""
step "typed now, the flag comes off" "$(issue $new "$chore" chore needs-type)" 0 0 "$remove"
step "a bug with no severity is flagged and told" "$(issue $new "$bug" bug)" 0 0 "$tell"
if ! grep -qxF '<!-- spool:needs-headings -->' "$stub/note" || ! grep -qF -- '- label: severity:*' "$stub/note"; then
	echo "FAIL the comment carries its marker and what is missing"
	fails=$((fails + 1))
elif grep -qF 'Observed behavior' "$stub/note"; then
	echo "FAIL the comment names a heading that is there"
	fails=$((fails + 1))
else
	echo "ok   the comment names only what is missing"
fi
step "told before, no second comment" "$(issue $new "secret body" chore)" 1 0 "$add"
if grep -qF 'secret body' "$stub/note"; then
	echo "FAIL the body is echoed"
	fails=$((fails + 1))
fi
step "flagged and told, a failing edit is quiet" "$(issue $new "x" chore needs-type)" 1 0 ""
step "the severity label clears it" "$(issue $new "$bug" bug severity:high needs-type)" 0 0 "$remove"
step "filed before the rule, not held" "$(issue $old "x" bug)" 0 0 ""
step "filed before the rule, an old flag comes off" "$(issue $old "x" bug needs-type)" 0 0 "$remove"
step "a null body" '{"created_at":"2026-11-11T09:00:00Z","body":null,"labels":[{"name":"chore"}]}' 0 0 "$tell"
step "a failed comments read writes nothing and fails the step" "$(issue $new "x" chore)" fail 1 ""

[ "$fails" -eq 0 ] || { echo "$fails case(s) failed"; exit 1; }
echo "all cases pass"
