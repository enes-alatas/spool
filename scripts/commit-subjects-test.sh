#!/usr/bin/env bash
# Fixtures for scripts/commit-subjects.sh (#1011): the subjects it must
# refuse, the next obvious phrasings of each, and subjects from main it
# must leave alone, since a check that fires on a correct commit gets
# switched off. Messages are synthetic.
set -uo pipefail

cd "$(dirname "$0")/.."
fails=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# check wants the script's exit for one commit, and when it refuses, a
# phrase of what it says.
check() { # want-exit label message [phrase] [author] [parents]
	local got=0 said
	jq -cn --arg message "$3" --arg author "${5:-enes-alatas}" --argjson parents "${6:-1}" \
		'{sha: "0123456789abcdef", author: $author, parents: $parents, message: $message}' > "$work/commits.jsonl"
	said=$(bash scripts/commit-subjects.sh "$work/commits.jsonl") || got=$?
	if [ "$got" -ne "$1" ] || [[ -n ${4:-} && $said != *"$4"* ]]; then
		echo "FAIL $2: exit $got, want $1 ${4:+saying \"$4\" }($said)"
		fails=$((fails + 1))
	else
		echo "ok   $2"
	fi
}

long="feat(web): $(printf 'x%.0s' {1..69})"

# refused
check 1 "no type" "the fleet channel is a tab" "type(scope): summary"
check 1 "an unknown type" "build: pin the base image" "type(scope): summary"
check 1 "a capitalised type" "Fix(web): a refused send keeps its draft" "type(scope): summary"
check 1 "no space after the colon" "fix(web):a refused send" "type(scope): summary"
check 1 "an empty scope" "fix(): a refused send" "type(scope): summary"
check 1 "two scopes, comma" "feat(route,loop): a reply reaches its room" "more than one thing"
check 1 "two scopes, comma and space" "feat(route, loop): a reply reaches its room" "more than one thing"
check 1 "two scopes, plus" "feat(web+httpapi): a copy chip" "more than one thing"
check 1 "81 characters" "${long}x" "81 characters"
check 1 "closes in the body" $'fix(loop): a turn ends cleanly\n\nCloses #80.' "closing keyword"
check 1 "fixes in the subject" "fix(loop): fixes #12 by ending the turn" "closing keyword"
check 1 "keyword with a colon" $'fix(loop): a turn ends\n\nResolves: #7' "closing keyword"
check 1 "closed, mid-sentence" $'docs: the plan\n\nThis closed #80 by mistake.' "closing keyword"
check 1 "fixed, capitalised" $'test(itest): one wait\n\nFixed #995.' "closing keyword"
check 1 "owner/repo#n" $'fix(loop): a turn ends\n\nCloses enes-alatas/spool#12' "closing keyword"
check 1 "an issue URL" $'fix(loop): a turn ends\n\nFixes https://github.com/enes-alatas/spool/issues/12' "closing keyword"
check 1 "does not close, still closes" $'docs: the plan\n\nThis does not close #253.' "closing keyword"

# let through
check 0 "a scope" "feat(web): the fleet channel is a tab of the Fleet page"
check 0 "no scope" "chore: the model lists know Opus 5.5 and Fable 5.1"
check 0 "a nested scope" "fix(surface/telegram): a bound group keeps its room"
check 0 "a breaking marker" "feat(httpapi)!: the loops route drops the old field"
check 0 "80 characters" "$long"
check 0 "Part of in the body" $'feat(ci): a check\n\nPart of #472.'
check 0 "an issue named in prose" $'fix(itest): one bound\n\nThe bound #714 set, settling #80'"'"'s questions.'
check 0 "a keyword far from the number" $'fix(loop): a turn ends\n\nThis fixes the case #12 describes.'
check 0 "a word ending in fix" $'refactor: prefix #3 stays\n\nSuffix #4 too.'
check 0 "Dependabot's own subject" "chore(deps-dev): Bump vite from 7.3.6 to 7.3.7 in /web in the npm_and_yarn group across 1 directory" "" "dependabot[bot]"
check 0 "an em dash counts once" "fix(web): $(printf '—%.0s' {1..69})"
check 0 "hotfix is not fix" $'fix(loop): a turn ends\n\nA hotfix #12 shipped.'
check 0 "a pull request URL" $'fix(loop): a turn ends\n\nFixes the case in https://github.com/o/r/pull/12.'
check 0 "an Update branch merge commit" "Merge branch 'main' into fix/thing" "" "" 2

# more than one commit: each refusal names its commit
jq -cn '{sha: "aaaaaaaa11", author: "x", message: "feat(web): fine"}, {sha: "bbbbbbbb22", author: "x", message: "oops"}' > "$work/commits.jsonl"
said=$(bash scripts/commit-subjects.sh "$work/commits.jsonl")
if [ $? -eq 1 ] && [[ $said == *"bbbbbbbb: "* && $said != *"aaaaaaaa"* ]]; then
	echo "ok   the refusal names the commit"
else
	echo "FAIL the refusal names the commit: $said"
	fails=$((fails + 1))
fi
if bash scripts/commit-subjects.sh "$work/missing.jsonl" >/dev/null; [ $? -eq 2 ]; then
	echo "ok   unreadable input"
else
	echo "FAIL unreadable input"
	fails=$((fails + 1))
fi

[ "$fails" -eq 0 ] || { echo "$fails case(s) failed"; exit 1; }
echo "all cases pass"
