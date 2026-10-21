#!/usr/bin/env bash
# Fixtures for scripts/fleet-mention.sh and the step around it,
# scripts/fleet-mention-flag.sh (#630). The step runs as Actions runs a
# `run:` block, under `bash -e`, with a stub `gh` that records the calls it
# would make. Names and bodies are synthetic.
set -uo pipefail

cd "$(dirname "$0")/.."
fails=0

# match wants fleet-mention.sh's exit for a body, and the name it prints.
match() { # want-exit want-name label body
	local got=0 said
	said=$(printf '%s' "$4" | bash scripts/fleet-mention.sh) || got=$?
	if [ "$got" -ne "$1" ] || [ "$said" != "$2" ]; then
		echo "FAIL $3: exit $got, said \"$said\"; want $1, \"$2\""
		fails=$((fails + 1))
	else
		echo "ok   $3"
	fi
}

match 1 terra "a mention" "over to @terra, thanks"
match 1 Milo "capitalised, after a paren" "(cc @Milo)"
match 1 quinn "at the start" "@quinn see above"
match 1 enesalatas "a person's handle" "waiting on @enesalatas."
match 1 iris "after a fenced block closes" $'```\n@terra\n```\nand @iris'
match 0 "" "no @" "over to terra"
match 0 "" "an address" "write to ops@terra.example"
match 0 "" "a longer word" "see @terraform docs"
match 0 "" "a code span" 'the hook refuses `@terra` here'
match 0 "" "a fenced block" $'```sh\ngh pr comment 1 --body "@terra"\n```'
match 0 "" "a tilde fence" $'~~~\n@milo\n~~~'
match 0 "" "an empty body" ""
match 1 quinn "a ~~~ block showing a \`\`\` line" $'~~~\n```\n~~~\nthen @quinn'
match 1 milo "an indented \`\`\` line is not a fence" $'text\n    ```\nthen @milo'
match 0 "" "a \`\`\`\` block holding a \`\`\` line" $'````\n```\n@terra\n````'
match 1 iris "a shorter run doesn't close" $'````\n```\n````\n@iris'
match 1 terra "a closing run with text after it doesn't close" $'```\n``` sh\n@milo\n```\n@terra'
match 0 "" "an HTML comment" '<!-- ask @terra --> done'
match 0 "" "an HTML comment over lines" $'<!--\n@quinn\n-->'
match 1 iris "after an HTML comment closes" $'<!--\n@quinn\n--> and @iris'
match 0 "" "a URL" 'see https://github.com/@terra'

stub=$(mktemp -d)
trap 'rm -rf "$stub"' EXIT
cat > "$stub/gh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$CALLS"
if [[ "$*" != *--method* && "$*" == *--jq* ]]; then
	cat "${FETCH:-/dev/null}"
fi
STUB
chmod +x "$stub/gh"

# step runs the flag step on one event file and wants its exit and the
# label call, or none.
step() { # label event-name event-json want-exit want-call [fetched]
	local got=0 calls
	printf '%s' "$3" > "$stub/event.json"
	printf '%s' "${6:-}" > "$stub/fetch.json"
	: > "$stub/calls"
	PATH="$stub:$PATH" CALLS="$stub/calls" FETCH="$stub/fetch.json" \
		GITHUB_REPOSITORY=o/r GITHUB_EVENT_NAME="$2" GITHUB_EVENT_PATH="$stub/event.json" \
		bash -e scripts/fleet-mention-flag.sh >/dev/null 2>&1 || got=$?
	calls=$(grep -- '--method POST' "$stub/calls" || true)
	if [ "$got" -ne "$4" ] || [ "$calls" != "$5" ]; then
		echo "FAIL $1: exit $got, calls \"$calls\"; want $4, \"$5\""
		fails=$((fails + 1))
	else
		echo "ok   $1"
	fi
}

label="api --method POST repos/o/r/issues/206/labels -f labels[]=fleet-mention --silent"
step "an issue body" issues '{"issue":{"number":206,"body":"cc @terra"}}' 0 "$label"
step "an issue body with none" issues '{"issue":{"number":206,"body":"cc terra"}}' 0 ""
step "an issue with no body" issues '{"issue":{"number":206,"body":null}}' 0 ""
step "a comment, not the issue's body" issue_comment \
	'{"issue":{"number":206,"body":"@terra"},"comment":{"id":9,"body":"clean"}}' 0 ""
step "a comment with one" issue_comment \
	'{"issue":{"number":206,"body":"clean"},"comment":{"id":9,"body":"thanks @iris"}}' 0 "$label"
step "a review comment" pull_request_review_comment \
	'{"pull_request":{"number":206,"body":"clean"},"comment":{"id":9,"body":"@quinn?"}}' 0 "$label"
step "a pull request's body" pull_request \
	'{"pull_request":{"number":206,"body":"over to @milo"}}' 0 "$label"
step "a dispatch fetches the issue" workflow_dispatch \
	'{"inputs":{"issue":"206","kind":"issue"}}' 0 "$label" '{"issue":{"number":206,"body":"@elliot"}}'
step "a dispatch fetches the comment" workflow_dispatch \
	'{"inputs":{"issue":"206","comment":"9","kind":"issue_comment"}}' 0 "$label" \
	'{"issue":{"number":206},"comment":{"id":9,"body":"@terra"}}'
step "a dispatch with none" workflow_dispatch \
	'{"inputs":{"issue":"206","kind":"issue"}}' 0 "" '{"issue":{"number":206,"body":"clean"}}'

[ "$fails" -eq 0 ] || { echo "$fails case(s) failed"; exit 1; }
echo "all cases pass"
