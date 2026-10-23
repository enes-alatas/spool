#!/usr/bin/env bash
# Exercises scripts/security-paths.sh against the real pattern list (#742).
#
# The matches are PRs that needed a security review, #741 first: it changed
# only internal/runtime/docker, which the guard list kept in a mission
# prompt did not name. The misses are PRs the label must leave alone, since
# a label on every PR is a label nobody reads.
set -uo pipefail

cd "$(dirname "$0")/.."
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# name, want (match, none or error), patterns file, changed files.
check() {
	local name=$1 want=$2 patterns=$3 files=$4 status got
	printf '%s\n' "$files" >"$work/files"
	bash scripts/security-paths.sh "$patterns" "$work/files" >"$work/out" 2>/dev/null
	status=$?
	case $status in 0) got=match ;; 1) got=none ;; *) got=error ;; esac
	if [ "$got" = "$want" ]; then pass "$name"; else fail "$name: want $want, got $got"; fi
}

list=.github/security-review-paths

check "#741 workstation runtime only" match "$list" $'internal/runtime/docker/docker.go\nitest/docker_rebuild_test.go\ndocs/adr/0017-workstation-design.md'
check "power path in the actor" match "$list" $'internal/loop/actor.go'
check "link code route" match "$list" $'internal/route/link.go'
check "a workflow" match "$list" $'.github/workflows/pr-guards.yml'
check "the list itself" match "$list" $'.github/security-review-paths'
check "a secret guard" match "$list" $'scripts/secret-rules.awk'
check "the backup script" match "$list" $'scripts/spool-backup.sh'
check "a script a write token runs" match "$list" $'scripts/advisories.sh'
check "a Go dependency" match "$list" $'go.sum'
check "a web dependency" match "$list" $'web/package-lock.json'
check "workstation image" match "$list" $'docker/workstation/Dockerfile'
check "deep under a pattern" match "$list" $'internal/surface/telegram/poll/x.go'
check "the hub binary" match "$list" $'cmd/spool/main.go'
check "the GitHub App minter" match "$list" $'internal/githubapp/githubapp.go'
check "the workstation GitHub helper" match "$list" $'internal/githubhelper/githubhelper.go'
check "the GitHub helper binary" match "$list" $'cmd/spool-github/main.go'
check "a loop's GitHub env" match "$list" $'internal/loop/github.go'
# The job feeds a rename's old path as well as its new one (pr-guards).
check "a rename out of a listed path" match "$list" $'internal/tools/guard.go\ninternal/hook/guard.go'
check "web page only" none "$list" $'web/src/pages/LoopDetail.tsx\nweb/src/styles.css'
check "docs only" none "$list" $'docs/OPERATIONS.md\ndocs/adr/0050-link.md'
check "loop internals but not the actor" none "$list" $'internal/loop/prompt.go\nitest/prompt_test.go'
check "a script no write token runs" none "$list" $'scripts/other-half.sh'
check "prefix is not a directory" none "$list" $'internal/storekeeper/x.go\ndocker-compose.yml'
check "no changed files" none "$list" ''

printf '# only a comment\n\n' >"$work/empty"
check "an emptied list is an error, not silence" error "$work/empty" $'internal/httpapi/api.go'
check "a missing list is an error" error "$work/missing" $'internal/httpapi/api.go'

printf 'internal/egress/*   # trailing comment\n' >"$work/commented"
check "a trailing comment is not part of the pattern" match "$work/commented" $'internal/egress/proxy.go'

# The output names the matching paths, so the job log says why.
printf 'internal/egress/proxy.go\nREADME.md\n' >"$work/files"
out=$(bash scripts/security-paths.sh "$list" "$work/files")
if [ "$out" = "internal/egress/proxy.go" ]; then pass "prints only the matching paths"; else fail "prints only the matching paths: got '$out'"; fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failure(s)"
	exit 1
fi
