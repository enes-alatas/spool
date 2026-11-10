#!/usr/bin/env bash
# Exercises scripts/claude-code-watch.sh against fixture apt indexes (#1454).
#
# The index is the shape Anthropic's repo serves: one stanza per version,
# each `Version: X.Y.Z-N`, not in version order. `gh` is stubbed: it answers
# the issue listing by applying the script's own --jq to the issues in
# $STUB_DIR/issues.json, as GitHub returns them, and records every call, so
# the filter is tested as written and nothing
# here reaches GitHub.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DIR/calls"
[ -z "${STUB_FAIL:-}" ] || { echo 'gh: HTTP 502' >&2; exit 1; }
case $1 in
api)
	for ((i = 1; i <= $#; i++)); do
		j=$((i + 1))
		[ "${!i}" != --jq ] || expr=${!j}
	done
	jq -r "$expr" "$STUB_DIR/issues.json"
	;;
esac
exit 0
STUB
chmod +x "$work/bin/gh"
export PATH=$work/bin:$PATH STUB_DIR=$work GITHUB_REPOSITORY=o/r

# One issue as GitHub lists it: number, state, the version its marker asks
# for, and who wrote it (the workflow's bot unless named).
issue() {
	jq -n --argjson n "$1" --arg s "$2" --arg v "$3" --arg u "${4:-github-actions[bot]}" --arg a "${5:-NONE}" \
		'{number: $n, state: $s, user: {login: $u}, author_association: $a,
		  body: "### What needs doing\n\n<!-- claude-code-watch \($v) -->\n"}'
}

stanza() { printf 'Package: %s\nVersion: %s\nArchitecture: amd64\n\n' "$1" "$2"; }
{
	stanza claude-code 2.1.318-1
	stanza claude-code 2.1.321-1
	stanza claude-code 2.1.9-1
	stanza claude-code-extras 9.9.9-1
	stanza claude-code 2.1.319-1
} >"$work/index"
printf '2.1.310\n' >"$work/pin"

# name, want (file, update, quiet or error), pin; the issue listing on stdin
# as the script's --jq prints it (number, state, version).
check() {
	local name=$1 want=$2 pin=$3 out status
	jq -s . >"$work/issues.json"
	: >"$work/calls"
	printf '%s\n' "$pin" >"$work/pin"
	out=$(bash "$root/scripts/claude-code-watch.sh" --packages "${INDEX:-$work/index}" --pin "$work/pin" 2>&1)
	status=$?
	case $want in
	file)
		if [ "$status" -ne 0 ] || ! grep -q "^issue create -R o/r --title ci(workstation): bump the Claude Code pin from $pin to 2.1.321 --label chore" "$work/calls"; then
			fail "$name: want a filing for 2.1.321, got status $status: $out / $(cat "$work/calls")"
		else pass "$name"; fi ;;
	update)
		if [ "$status" -ne 0 ] || ! grep -q "^issue edit 1460 -R o/r --title ci(workstation): bump the Claude Code pin from $pin to 2.1.321" "$work/calls" || grep -q '^issue create' "$work/calls"; then
			fail "$name: want #1460 updated to 2.1.321, got status $status: $out / $(cat "$work/calls")"
		else pass "$name"; fi ;;
	quiet)
		if [ "$status" -ne 0 ] || grep -q '^issue' "$work/calls"; then
			fail "$name: want nothing filed, got status $status: $out / $(cat "$work/calls")"
		else pass "$name"; fi ;;
	error)
		if [ "$status" -ne 1 ] || grep -q '^issue' "$work/calls"; then
			fail "$name: want a failed run and nothing filed, got status $status: $out"
		else pass "$name"; fi ;;
	esac
}

# The repo is ahead: newest by version, not by line or by text order
# (2.1.9 sorts after 2.1.321 as text), and only claude-code's stanzas.
check newer-files file 2.1.310 </dev/null
check older-issues-closed-files file 2.1.310 < <(issue 1300 closed 2.1.300)
check open-older-is-updated update 2.1.310 < <(issue 1460 open 2.1.319)

# Nothing to ask for.
check pin-is-newest quiet 2.1.321 </dev/null
check pin-ahead-of-repo quiet 2.1.330 </dev/null
check already-open quiet 2.1.310 < <(issue 1461 open 2.1.321)
check already-declined quiet 2.1.310 < <(issue 1461 closed 2.1.321)

# A stranger's issue holding the marker counts for nothing: it neither
# silences the ask nor is rewritten by it (#1456). A maintainer's does.
check stranger-cannot-silence file 2.1.310 < <(issue 1470 closed 2.1.321 someone NONE)
check stranger-is-not-rewritten file 2.1.310 < <(issue 1460 open 2.1.319 someone CONTRIBUTOR)
check maintainer-marker-counts quiet 2.1.310 < <(issue 1471 open 2.1.321 enes-alatas OWNER)
check bot-and-stranger update 2.1.310 < <(issue 1470 open 2.1.300 someone NONE; issue 1460 open 2.1.319)

# A check that can't run is red, and files nothing.
check bad-pin error 'v2.1' </dev/null
printf 'Package: something-else\nVersion: 1.0.0-1\n' >"$work/other"
INDEX=$work/other check not-the-index error 2.1.310 </dev/null
printf 'Package: claude-code\nVersion: 2.1.400$(id)-1\n' >"$work/forged"
INDEX=$work/forged check forged-version error 2.1.310 </dev/null
STUB_FAIL=1 check listing-fails error 2.1.310 </dev/null
INDEX=$work/missing check index-missing error 2.1.310 </dev/null

# --dry-run prints the body and writes nothing.
echo [] >"$work/issues.json"
: >"$work/calls"
printf '2.1.310\n' >"$work/pin"
out=$(bash "$root/scripts/claude-code-watch.sh" --packages "$work/index" --pin "$work/pin" --dry-run 2>&1)
if [ $? -eq 0 ] && ! grep -q '^issue' "$work/calls" && grep -q '<!-- claude-code-watch 2.1.321 -->' <<<"$out" &&
	grep -q '^### What needs doing' <<<"$out" && grep -q 'internal/loop/models.go' <<<"$out"; then
	pass dry-run
else
	fail "dry-run: $out"
fi

if [ "$failures" -gt 0 ]; then
	echo "claude-code-watch-test: $failures failure(s)"
	exit 1
fi
echo "claude-code-watch-test: all passed"
