#!/usr/bin/env bash
# gate-annotate.sh TITLE -- CMD [ARG...] runs a CI gate and, when it fails,
# prints one GitHub error annotation titled TITLE that carries the last
# lines the gate wrote (#1373). A loop's box cannot fetch job logs, so a red
# gate that only says "exit code 1" names nothing; check-run annotations can
# be read through the API.
#
# CMD reads this script's stdin; its stderr joins its stdout, which passes
# through to the job log, and its exit code is this script's. A gate that
# printed its own `::error` line (scripts/test-failures.sh,
# scripts/skill-tier1-gate.sh) gets no second one.
#
# The repository is public, so its job logs are too: the annotation copies
# lines anyone could read already. The message is escaped as a workflow
# command's data, so a line cannot end the annotation and start another
# command.
set -uo pipefail

usage() { echo "usage: gate-annotate.sh TITLE -- CMD [ARG...]" >&2; exit 2; }
(($# >= 3)) || usage
title=$1
[[ $2 == -- ]] || usage
shift 2

lines=${GATE_ANNOTATE_LINES:-15}

data() {
	local s=${1//%/%25}
	s=${s//$'\r'/%0D}
	printf '%s' "${s//$'\n'/%0A}"
}
property() {
	local s
	s=$(data "$1")
	s=${s//:/%3A}
	printf '%s' "${s//,/%2C}"
}

out=$(mktemp)
trap 'rm -f "$out"' EXIT

"$@" 2>&1 | tee "$out"
rc=${PIPESTATUS[0]}

((rc == 0)) && exit 0
grep -q '^::error' "$out" && exit "$rc"

tail_text=$(grep -v '^[[:space:]]*$' "$out" | grep -v '^::' | tail -n "$lines")
[[ -n $tail_text ]] || tail_text="exited $rc and printed nothing"
printf '::error title=%s::%s\n' "$(property "$title")" "$(data "$tail_text")"
exit "$rc"
