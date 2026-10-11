#!/usr/bin/env bash
# Exercises scripts/advisories.sh against the tools' real output (#737).
#
# The fixtures in testdata/advisories are what govulncheck v1.9.0 and npm
# audit wrote, not hand-typed shapes: govulncheck scanned this repo against a
# local database holding synthetic advisories (GO-0000-*), and npm audited a
# throwaway lockfile pinning a minimist with two published advisories. `gh`
# is stubbed and records every call, so nothing here reaches GitHub.
set -uo pipefail

cd "$(dirname "$0")/.."
data=scripts/testdata/advisories
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# The stub answers the chore-issue listing with $work/filed (bodies, one per
# line, as --jq prints them) and records each call in $work/calls.
mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DIR/calls"
case $1 in
api) cat "$STUB_DIR/filed" 2>/dev/null ;;
issue)
	for ((i = 1; i <= $#; i++)); do
		if [ "${!i}" = --body-file ]; then
			j=$((i + 1))
			cat "${!j}" >>"$STUB_DIR/bodies"
		fi
	done
	;;
esac
STUB
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH" STUB_DIR="$work" GITHUB_REPOSITORY=o/r GITHUB_RUN_ID=42

run() { # go json, npm json, extra args...
	rm -f "$work/calls" "$work/bodies"
	local go=$1 npm=$2
	shift 2
	bash scripts/advisories.sh --go "$go" --npm "$npm" "$@" >"$work/out" 2>&1
}
creates() { grep -c '^issue create' "$work/calls" 2>/dev/null || true; }

# 1. A called Go advisory, a called stdlib one, and the two npm advisories
# are filed; the advisory that is only imported is not.
: >"$work/filed"
if run "$data/govulncheck.json" "$data/npm-audit.json" && [ "$(creates)" = 4 ]; then
	pass "four advisories filed"
else
	fail "four advisories filed (got $(creates)): $(cat "$work/out")"
fi
for id in GO-0000-0001 GO-0000-0002 GHSA-vh95-rmgr-6w4m GHSA-xvch-5gv4-984h; do
	grep -q -- "--title Advisory $id: " "$work/calls" || fail "$id has its own issue"
done
if grep -q GO-0000-0003 "$work/calls" "$work/bodies"; then
	fail "an advisory only imported is filed"
else
	pass "an advisory only imported is not filed"
fi

# 2. Each issue passes the typing gate: exactly one label, chore, and the
# chore form's three headings.
if [ "$(grep '^issue create' "$work/calls" | grep -c -- '--label chore --body-file')" = 4 ] &&
	! grep '^issue create' "$work/calls" | grep -E -- '--label .*--label ' >/dev/null; then
	pass "every issue is labelled chore and nothing else"
else
	fail "every issue is labelled chore and nothing else: $(cat "$work/calls")"
fi
for heading in '### What needs doing' '### Where it lives' '### Out of scope'; do
	[ "$(grep -cxF "$heading" "$work/bodies")" = 4 ] || fail "every body has '$heading'"
done
if grep -q 'go1.26.9; fixed in go1.26.10' "$work/bodies" && grep -q 'toolchain line' "$work/bodies"; then
	pass "a stdlib advisory asks for the toolchain bump"
else
	fail "a stdlib advisory asks for the toolchain bump"
fi
# No call path: the trace's Spool frames stay in the run log.
if grep -q 'internal/surface' "$work/bodies"; then
	fail "a body names Spool's call path"
else
	pass "no body names Spool's call path"
fi

# 3. An advisory already filed, open or closed, is not filed again; the
# marker in a body is what says so.
printf '%s\n' 'old text' '<!-- advisory: GO-0000-0001 -->' '<!-- advisory: GHSA-xvch-5gv4-984h -->' >"$work/filed"
if run "$data/govulncheck.json" "$data/npm-audit.json" && [ "$(creates)" = 2 ] &&
	! grep -q -- '--title Advisory GO-0000-0001' "$work/calls" &&
	! grep -q -- '--title Advisory GHSA-xvch' "$work/calls"; then
	pass "an advisory already filed is skipped"
else
	fail "an advisory already filed is skipped (got $(creates)): $(cat "$work/calls")"
fi
: >"$work/filed"

# 4. A dry run calls nothing, and prints what it would file.
if run "$data/govulncheck.json" "$data/npm-audit.json" --dry-run && [ ! -s "$work/calls" ] &&
	[ "$(grep -c '^would file: ' "$work/out")" = 4 ]; then
	pass "a dry run files nothing and says what it would"
else
	fail "a dry run files nothing and says what it would: $(cat "$work/out")"
fi

# 5. Clean reports file nothing and do not even list issues.
if run "$data/govulncheck-clean.json" "$data/npm-audit-clean.json" && [ ! -s "$work/calls" ]; then
	pass "clean reports file nothing"
else
	fail "clean reports file nothing: $(cat "$work/out")"
fi

# 6. A scan that did not run is red, not clean: an npm error report, an
# unversioned one, an empty govulncheck file, and a file that is not JSON.
printf '{"error":{"code":"EAI_AGAIN","summary":"audit endpoint returned an error"}}\n' >"$work/npm-error.json"
: >"$work/empty.json"
printf 'govulncheck: fetching vulnerabilities: Forbidden\n' >"$work/text.json"
# Shaped like a report but unversioned: only the version check refuses it.
printf '{"vulnerabilities":{}}\n' >"$work/npm-unversioned.json"
for broken in "$data/govulncheck.json $work/npm-error.json" "$data/govulncheck.json $work/npm-unversioned.json" "$work/empty.json $data/npm-audit-clean.json" "$work/text.json $data/npm-audit-clean.json"; do
	set -- $broken
	if run "$1" "$2"; then
		fail "a failed scan is an error ($(basename "$1") / $(basename "$2"))"
	elif [ -s "$work/calls" ]; then
		fail "a failed scan calls gh ($(basename "$1") / $(basename "$2"))"
	else
		pass "a failed scan is an error ($(basename "$1") / $(basename "$2"))"
	fi
done

# 7. Upstream text cannot mention anyone: an @handle in a summary is broken
# with a zero-width space, and a scoped npm name sits in backticks.
jq -c 'if .osv then .osv.summary = "Reported by @finder" else . end' "$data/govulncheck.json" >"$work/gv-handle.json"
jq '.vulnerabilities.minimist.via[0].title = "Thanks @finder" | .vulnerabilities.minimist.via[0].name = "@scope/pkg" | .vulnerabilities.minimist.name = "@scope/pkg"' "$data/npm-audit.json" >"$work/npm-handle.json"
run "$work/gv-handle.json" "$work/npm-handle.json"
if grep -q '@finder' "$work/bodies"; then
	fail "an @handle in an advisory's text reaches the body"
elif ! grep -q $'@\xe2\x80\x8bfinder' "$work/bodies"; then
	fail "an @handle in an advisory's text is kept, defused"
else
	pass "an @handle in an advisory's text is defused"
fi
if grep -q '`@scope/pkg`' "$work/bodies" && ! sed 's/`[^`]*`//g' "$work/bodies" | grep -q '@scope'; then
	pass "a scoped npm name is only ever in backticks"
else
	fail "a scoped npm name is only ever in backticks: $(grep -n scope "$work/bodies")"
fi

# 8. The once-ever check reads every issue, not only those still labelled
# chore: triage may retype an advisory, and it must not be filed again.
if grep -q '^api ' "$work/calls" && ! grep '^api ' "$work/calls" | grep -q labels=; then
	pass "every issue is read for the marker, whatever its label"
else
	fail "every issue is read for the marker, whatever its label: $(grep '^api ' "$work/calls")"
fi

if [ "$failures" -gt 0 ]; then
	printf '\n%d failure(s)\n' "$failures"
	exit 1
fi
printf '\nall passed\n'
