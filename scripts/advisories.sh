#!/usr/bin/env bash
# The filing step of .github/workflows/advisories.yml (#737): what the daily
# scan found becomes one chore issue per advisory, the same day, so a new
# advisory against code nobody is changing is seen before an unrelated PR
# turns red on it (#665).
#
# Two inputs, both as the tools write them:
#   --go   govulncheck -format json ./...       (a stream of JSON objects)
#   --npm  npm audit --omit=dev --json, in web/ (what the shipped room carries)
#
# What is filed:
# - Go: an advisory govulncheck finds Spool's code calling, the findings
#   whose trace starts at a function. One that is only imported or required
#   is what `govulncheck ./...` in CI does not fail on either, and Dependabot
#   alerts cover go.mod.
# - npm: every advisory against a runtime dependency of the control room.
#   Dev tooling is reported in the run's summary by the workflow, not filed.
#
# An issue is the chore form's three headings and exactly one type label,
# the gate every filing passes (ADR-0014). It carries the advisory's ids,
# versions and links, never a call path: whether Spool is exploitable is an
# assessment for the operator, in private, not for a public issue.
#
# Once per advisory, ever: each body carries a marker, and an issue holding
# it, open or closed and whatever triage relabelled it to, means the
# advisory was filed. Every issue is listed rather than searched, because the
# search index lags a fresh issue and the next run would file it again.
#
# An advisory's own words are someone else's text in an issue the project
# opens, so an `@handle` in them (a credit line) would notify that user: the
# summary gets a zero-width space after each `@`, and every package name,
# npm's `@scope/name` included, sits in backticks, where GitHub mentions no one.
#
# A scan that did not run is a failure, not an empty result: an input that
# is not the tool's output exits 1, so the run goes red and ci-health posts
# it to the pinned CI-health issue (#210).
#
# Usage (the workflow sets GH_TOKEN and GITHUB_REPOSITORY):
#   advisories.sh --go gv.json --npm audit.json [--dry-run]
# --dry-run prints what it would file and calls nothing that writes.
set -euo pipefail

go_json="" npm_json="" dry_run=false
while [ $# -gt 0 ]; do
	case $1 in
	--go) go_json=$2; shift 2 ;;
	--npm) npm_json=$2; shift 2 ;;
	--dry-run) dry_run=true; shift ;;
	*) echo "advisories: unknown argument $1" >&2; exit 2 ;;
	esac
done
if [ -z "$go_json" ] || [ -z "$npm_json" ]; then
	echo "usage: advisories.sh --go FILE --npm FILE [--dry-run]" >&2
	exit 2
fi

# govulncheck writes its config first; a file without one is an error page,
# a crash, or nothing at all.
if ! jq -s -e 'any(.[]; has("config"))' "$go_json" >/dev/null 2>&1; then
	echo "advisories: $go_json is not govulncheck output" >&2
	exit 1
fi
# npm audit exits 1 both on findings and on a failed audit, so its exit code
# says nothing; a report says which.
if ! jq -e '.auditReportVersion == 2' "$npm_json" >/dev/null 2>&1; then
	echo "advisories: $npm_json is not an npm audit report" >&2
	exit 1
fi

run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-}/actions/runs/${GITHUB_RUN_ID:-}"

# One line per advisory: {id, title, body}. Built by jq from the reports, so
# nothing an advisory says passes through the shell.
plan=$(
	{
		jq -s -c --arg run "$run_url" '
			def quiet: gsub("@"; "@\u200b");
			(map(select(.osv)) | map({key: .osv.id, value: .osv}) | from_entries) as $osv
			| map(select(.finding and .finding.trace[0].function != null) | .finding)
			| group_by(.osv)[]
			| .[0] as $f
			| $f.trace[0] as $at
			| ($osv[$f.osv] // {}) as $o
			| ($at.module == "stdlib") as $std
			| def gover: if $std and . != null then "go" + ltrimstr("v") else . end;
			{
				id: $f.osv,
				title: ("Advisory " + $f.osv + ": "
					+ (if $std then "the Go toolchain " + ($at.version | gover // "?") + " needs " + ($f.fixed_version | gover // "a fix")
					   else $at.module + " " + ($at.version // "?") + " needs " + ($f.fixed_version // "a fix") end)),
				what: (($o.summary // "No summary." | quiet) + "\n\n"
					+ "- **Advisory:** " + $f.osv
					+ (if ($o.aliases // []) | length > 0 then " (" + ($o.aliases | join(", ")) + ")" else "" end)
					+ ", " + ($o.database_specific.url // ("https://pkg.go.dev/vuln/" + $f.osv)) + "\n"
					+ "- **Found:** " + (if $std then "" else "`" + $at.module + "` " end) + ($at.version | gover // "?")
					+ "; fixed in " + ($f.fixed_version | gover // "no release yet") + "\n"
					+ "- **Reached:** govulncheck finds Spool calling an affected symbol\n"
					+ "- **Scan:** " + $run + "\n\n"
					+ (if $std then "Bump the `toolchain` line in go.mod to the fixed release."
					   else "Adopt the Dependabot PR if there is one; otherwise `go get " + $at.module + "@" + ($f.fixed_version // "latest") + " && go mod tidy`." end)),
				where: (if $std then "`go.mod` (toolchain line), and CI'"'"'s `go-version-file`." else "`go.mod`, `go.sum`." end)
			}
		' "$go_json"
		jq -c --arg run "$run_url" '
			def quiet: gsub("@"; "@\u200b");
			[.vulnerabilities[] as $v | $v.via[] | objects | {pkg: $v.name, fix: $v.fixAvailable, via: .}]
			| group_by(.via.url)[]
			| .[0] as $a
			| ($a.via.url | split("/") | last) as $id
			| {
				id: $id,
				title: ("Advisory " + $id + ": " + $a.via.name + " " + $a.via.range + " in the control room (" + $a.via.severity + ")"),
				what: (($a.via.title // "No title." | quiet) + "\n\n"
					+ "- **Advisory:** " + $a.via.url + "\n"
					+ "- **Affected:** `" + $a.via.name + "` " + $a.via.range + ", severity " + $a.via.severity
					+ ", reached through " + ([.[].pkg] | unique | map("`" + . + "`") | join(", ")) + "\n"
					+ "- **Fix:** " + (if $a.fix == true then "available within the declared ranges"
						elif ($a.fix | type) == "object" then "`" + $a.fix.name + "` " + $a.fix.version + (if $a.fix.isSemVerMajor then " (a major bump)" else "" end)
						else "none published yet" end) + "\n"
					+ "- **Scan:** " + $run + "\n\n"
					+ "Adopt the Dependabot PR if there is one; otherwise update it in `web/` (`npm update " + $a.via.name + "`, or an `overrides` entry for a transitive one)."),
				where: "`web/package.json`, `web/package-lock.json`."
			}
		' "$npm_json"
	}
)

if [ -z "$plan" ]; then
	echo "advisories: nothing to file"
	exit 0
fi

filed=""
if [ "$dry_run" = false ]; then
	filed=$(gh api --paginate "repos/$GITHUB_REPOSITORY/issues?state=all&per_page=100" --jq '.[].body // ""')
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
while IFS= read -r line; do
	id=$(jq -r .id <<<"$line")
	marker="<!-- advisory: $id -->"
	if [ -n "$filed" ] && grep -qF -- "$marker" <<<"$filed"; then
		echo "advisories: $id is already filed"
		continue
	fi
	title=$(jq -r .title <<<"$line")
	jq -r --arg marker "$marker" '
		"### What needs doing\n\n" + .what + "\n\n"
		+ "### Where it lives\n\n" + .where + "\n\n"
		+ "### Out of scope\n\n"
		+ "Anything beyond the bump. Whether this makes Spool exploitable is assessed in private, never in this issue.\n\n"
		+ "Filed by the `advisories` workflow (#737); the routine is in docs/QUALITY.md.\n"
		+ $marker
	' <<<"$line" >"$work/body"
	if [ "$dry_run" = true ]; then
		printf '%s\n\n' "would file: $title"
		cat "$work/body"
		echo
		continue
	fi
	gh issue create -R "$GITHUB_REPOSITORY" --title "$title" --label chore --body-file "$work/body"
done <<<"$plan"
