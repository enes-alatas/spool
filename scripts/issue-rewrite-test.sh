#!/usr/bin/env bash
# Fixtures for scripts/issue-rewrite.sh and the step around it,
# scripts/issue-rewrite-flag.sh (#1035). The must-fail and must-pass titles
# are real ones (#1023, and #1030 to #1033, which rewrote it); the rest are
# the next phrasings each rule should catch or let through. The step runs as
# Actions runs a `run:` block, under `bash -e`, with a stub `gh`.
set -uo pipefail

cd "$(dirname "$0")/.."
fails=0

form() { printf '### Current behavior and friction\n\n%s\n\n### Coupling\n\nPart of #860.\n' "$1"; }
clean=$(form "The control room shows the raw problem name.")

# check wants issue-rewrite.sh's exit for a title and body, and the rules
# it names: the lines' first words, joined ("Title Body").
check() { # want-exit want-rules label title [body] [flag]
	local got=0 out rules
	out=$(printf '%s' "${5-$clean}" | bash scripts/issue-rewrite.sh ${6:+"$6"} "$4" 2>/dev/null) || got=$?
	rules=$(printf '%s\n' "$out" | grep . | cut -d: -f1 | paste -sd' ' -)
	if [ "$got" -ne "$1" ] || [ "$rules" != "$2" ]; then
		echo "FAIL $3: exit $got, rules \"$rules\"; want $1, \"$2\""
		printf '%s\n' "$out" | sed 's/^/     /'
		fails=$((fails + 1))
	else
		echo "ok   $3"
	fi
}

# The real titles.
check 1 "Title Title Title Body" "#1023, title and opening" \
	"ADR-0059 slice 2, web half of PR 3a: the cloud_connections problem, and a cloud loop's connections on the connection page and delete dialog" \
	"$(form "Part of #860; web half of #977's PR 3a, the connections hand-off (ADR-0059 item 10).")"
check 0 "" "#1030" "The control room names a cloud session that started without its connections"
check 0 "" "#1031" "A connection's page says when a change reaches each cloud loop"
check 0 "" "#1032" "Deleting a cloud loop warns that its session keeps the connection values"
check 0 "" "#1033" "New loop's cloud note says how to give a cloud loop a repo"

# Rule 1, process names, and their next phrasings.
check 1 "Title" "an ADR in the middle" "The loop page shows the problems ADR-0059 adds"
check 1 "Title" "an ADR without the dash" "The loop page follows ADR 59 for problems"
check 1 "Title" "slices" "Fleet page slices for the pipeline view"
check 1 "Title" "a PR at the end" "Settings shows the fields from the PR"
check 1 "Title" "PRs" "The queue page lists the PRs a loop opened today"
check 1 "Title" "a hub half" "Hub half of the watchdog: missed wakes are recorded"
check 1 "Title" "a Go half" "Go half: turns past their time limit are recorded"
check 1 "Title" "part of" "Part of #754: the room shows a missed wake"
check 1 "Title" "an item number" "The delete dialog says what item 10 decided"
check 1 "Title" "an issue number" "Follow-up to #977: the room shows connections"
check 1 "Title" "a backticked symbol" 'The room shows `send_failed` in words'
check 1 "Title" "a snake_case symbol" "The room explains cloud_connections"
check 0 "" "a word with PR in it" "Prompt previews show the loop's mission"
check 0 "" "lowercase pr is a word, not a PR" "Settings keeps a loop's spr and npr values"
check 0 "" "a hyphenated word" "A loop's long-running turn is shown in the room"
check 0 "" "a team-only title may name its test" "test(itest): TestBootRecovery checks only the turns the crash left"
check 0 "" "a team-only title with a scope and a bang" "ci(issue-guards)!: needs-rewrite on a title with ADR-0059"
check 1 "Title" "a type the set doesn't hold" "wip: the room shows cloud_connections"
check 1 "Title" "a prefix needs its colon and space" "chore cloud_connections in the room"
check 1 "Title" "a capitalised type is a sentence, not a prefix" "Fix: the room shows cloud_connections"
check 1 "Title" "an all-caps type too" "DOCS(ops): the cloud listener behind ADR-0059"

# Rule 2, one deliverable.
check 1 "Title" "a comma and" "The room shows a missed wake, and Settings sets its grace"
check 1 "Title" "a semicolon" "The room shows a missed wake; Settings sets its grace"
check 1 "Title" "an ampersand" "The room shows a missed wake & Settings sets its grace"
check 0 "" "a list of three is one deliverable" "Pause, kill, rotate and power can wait forever on a loop that is shutting down"
check 0 "" "an and inside one phrase" "The room shows a loop's name and role on its row"
check 0 "" "one comma" "When a loop misses a wake, the room says so"
check 0 "" "one-deliverable overrides it" "The room shows a missed wake, and its grace period" "" --one-deliverable
check 1 "Title" "one-deliverable leaves rule 1" "The room shows a missed wake, and its slice" "" --one-deliverable
check 0 "" "a team-only title may list" "chore(deps): bump minimatch, zod, and vite" "$clean"

# Rule 3, length.
check 0 "" "100 characters" "$(printf 'A%.0s' {1..100})"
check 1 "Title" "101 characters" "$(printf 'A%.0s' {1..101})"
check 0 "" "100 characters, not bytes" "$(printf 'ğ%.0s' {1..100})"
check 1 "Title" "length binds team-only titles too" "chore: $(printf 'a%.0s' {1..100})"

# Rule 4, the body's opening.
check 1 "Body" "Part of after a blank line" "The room shows a missed wake" \
	"$(printf '### Current behavior and friction\n\n\n\nPart of #860, the watchdog.\n')"
check 1 "Body" "a bare #n" "The room shows a missed wake" "$(form "#754 asked for this.")"
check 1 "Body" "ADR-" "The room shows a missed wake" "$(form "ADR-0059 item 10 left this open.")"
check 1 "Body" "Split out of" "The room shows a missed wake" "$(form "Split out of #754.")"
check 1 "Body" "Web half of" "The room shows a missed wake" "$(form "Web half of #1144.")"
check 1 "Body" "Hub half of" "The room shows a missed wake" "$(form "Hub half of #1144.")"
check 1 "Body" "in bold" "The room shows a missed wake" "$(form "**Part of #754.**")"
check 1 "Body" "as a quote" "The room shows a missed wake" "$(form "> Part of #754.")"
check 1 "Body" "as a list item" "The room shows a missed wake" "$(form "- Part of #754.")"
check 1 "Body" "as a numbered item" "The room shows a missed wake" "$(form "1. Part of #754.")"
check 1 "Body" "as a numbered item with a paren" "The room shows a missed wake" "$(form "2) Part of #754.")"
check 1 "Body" "as a link" "The room shows a missed wake" "$(form "[Part of #754](https://github.com/o/r/issues/754)")"
check 1 "Body" "as a parenthesis" "The room shows a missed wake" "$(form "(Part of #754.)")"
check 0 "" "a number that opens a sentence" "The room shows a missed wake" "$(form "3 loops miss a wake each day.")"
check 1 "Body" "a follow-up" "The room shows a missed wake" "$(form "Follow-up to #1144.")"
check 1 "Body" "Parent:" "The room shows a missed wake" "$(form "Parent: #754")"
check 1 "Body" "CRLF line ends, as the web form sends" "The room shows a missed wake" \
	"$(printf '### Current behavior and friction\r\n\r\nPart of #754.\r\n')"
check 1 "Body" "no heading" "The room shows a missed wake" "Part of #754."
check 1 "Body" "a team-only issue too" "ci: run the guard" "$(form "Part of #630.")"
check 0 "" "a reference further down" "The room shows a missed wake" \
	"$(form "A loop that misses a wake is shown as fine. Part of #754.")"
check 0 "" "a sentence that mentions part of something" "The room shows a missed wake" \
	"$(form "The part of the row that shows health stays green.")"
check 0 "" "an empty body" "The room shows a missed wake" ""
check 2 "" "no title" "" "" ""

# Rule 5, acceptance scenarios (#1377), only with --scenarios.
feat() { printf '### Problem statement\n\nA loop that misses a wake is shown as fine.\n\n### Proposed shape\n\nThe row says so.\n\n%s### Out of scope\n\nAlerts.\n' "$1"; }
scen=$(feat $'### Acceptance scenarios\n\nWHEN a loop misses a wake THEN its row says missed (itest: TestMissedWake)\n\n')
check 0 "" "scenarios listed" "The room shows a missed wake" "$scen" --scenarios
check 1 "Body" "no scenarios heading" "The room shows a missed wake" "$(feat "")" --scenarios
check 1 "Body" "the form's empty answer" "The room shows a missed wake" \
	"$(feat $'### Acceptance scenarios\n\n_No response_\n\n')" --scenarios
check 1 "Body" "a heading with nothing under it before the next" "The room shows a missed wake" \
	"$(feat $'### Acceptance scenarios\n\n')" --scenarios
check 0 "" "without --scenarios no heading is needed" "The room shows a missed wake" "$(feat "")"
check 1 "Title" "--scenarios leaves the other rules" "The room shows a missed wake, and its grace" "$scen" --scenarios

stub=$(mktemp -d)
trap 'rm -rf "$stub"' EXIT
cat > "$stub/gh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$CALLS"
case "$*" in
api\ repos/*) cat "$FETCH" ;;
*--body-file*) cat "${@: -1}" > "$NOTE" ;;
esac
STUB
chmod +x "$stub/gh"

# step runs the flag step on one fetched issue and wants its exit and the
# writes it makes, joined by " | ".
step() { # label issue-json want-exit want-writes
	local got=0 calls
	printf '%s' "$2" > "$stub/fetch.json"
	: > "$stub/calls"
	: > "$stub/note"
	PATH="$stub:$PATH" CALLS="$stub/calls" FETCH="$stub/fetch.json" NOTE="$stub/note" \
		GITHUB_REPOSITORY=o/r NUM=7 bash -e scripts/issue-rewrite-flag.sh >/dev/null 2>&1 || got=$?
	calls=$(grep -v '^api ' "$stub/calls" | sed 's/--body-file .*/--body-file/' | paste -sd'|' - | sed 's/|/ | /g')
	if [ "$got" -ne "$3" ] || [ "$calls" != "$4" ]; then
		echo "FAIL $1: exit $got, writes \"$calls\"; want $3, \"$4\""
		fails=$((fails + 1))
	else
		echo "ok   $1"
	fi
}

issue() { # title body labels...
	local t=$1 b=$2
	shift 2
	jq -nc --arg t "$t" --arg b "$b" '{title: $t, body: $b, labels: ($ARGS.positional | map({name: .}))}' --args "$@"
}

bad="ADR-0059 slice 2: the room shows cloud_connections"
good="The room names a cloud session that started without its connections"
add="issue edit 7 -R o/r --add-label needs-rewrite | issue comment 7 -R o/r --body-file"
remove="issue edit 7 -R o/r --remove-label needs-rewrite"

step "a broken title is labelled and told once" "$(issue "$bad" "$clean" chore)" 0 "$add"
if ! grep -qF '<!-- spool:needs-rewrite -->' "$stub/note" || ! grep -qF -- '- Title: names' "$stub/note"; then
	echo "FAIL the comment carries its marker and the rule"
	fails=$((fails + 1))
elif grep -qE 'ADR-0059|cloud_connections' "$stub/note"; then
	echo "FAIL the comment echoes the title"
	fails=$((fails + 1))
else
	echo "ok   the comment names the rule and echoes nothing"
fi
step "already labelled, no second comment" "$(issue "$bad" "$clean" chore needs-rewrite)" 0 ""
step "fixed, the label comes off" "$(issue "$good" "$clean" chore needs-rewrite)" 0 "$remove"
step "clean and unlabelled, nothing" "$(issue "$good" "$clean" chore)" 0 ""
step "a community report is exempt" "$(issue "$bad" "Part of #1." community triage)" 0 ""
step "one-deliverable clears a joined title" \
	"$(issue "The room shows a missed wake, and its grace" "$clean" chore one-deliverable needs-rewrite)" 0 "$remove"
step "a null body" '{"title":"The room shows a missed wake","body":null,"labels":[]}' 0 ""

issue_at() { # created title body labels...
	local c=$1 t=$2 b=$3
	shift 3
	jq -nc --arg c "$c" --arg t "$t" --arg b "$b" '{created_at: $c, title: $t, body: $b, labels: ($ARGS.positional | map({name: .}))}' --args "$@"
}
step "a new feature filed with no scenarios is flagged" \
	"$(issue_at 2026-11-07T09:00:00Z "$good" "$(feat "")" "new feature")" 0 "$add"
step "a new feature with scenarios passes" \
	"$(issue_at 2026-11-07T09:00:00Z "$good" "$scen" "new feature")" 0 ""
step "a feature filed before the rule is not flagged" \
	"$(issue_at 2026-11-06T23:59:59Z "$good" "$(feat "")" "new feature")" 0 ""
step "an improvement needs none" \
	"$(issue_at 2026-11-07T09:00:00Z "$good" "$(feat "")" improvement)" 0 ""

[ "$fails" -eq 0 ] || { echo "$fails case(s) failed"; exit 1; }
echo "all cases pass"
