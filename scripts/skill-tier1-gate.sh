#!/usr/bin/env bash
# The gate over SkillEvaluator's tier 1 reports (ADR-0065, #1369).
#
# SkillEvaluator's own verdict fails every skill in this repo, for reasons
# that are about how our skills are written, not whether they are safe:
# SkillSpector calls a scan "incomplete" when a skill links to a file
# outside its own folder, and ours point at the docs of record; and the
# skills that teach security get malware and SSRF hits for naming what
# they teach. So the container's exit code is not the gate. This script is.
#
# Rule: every validator in each skill's report must say `passed`, except
# the two named in ADVISORY, whose status and findings go to the summary
# for the reviewer to read. A validator not on that list — including one a
# later image adds — gates, so a bump fails closed. `skipped`,
# `incomplete` and `error` are not `passed`: a check that could not run is
# red (#1369). Quality below the floor is the QUALITY validator failing.
# The advisory two must still have run (see the jq below). It reads
# structured fields only, never the text of a finding.
#
# It also refuses a run that scanned fewer skills than the tree holds, so a
# skill the evaluator never saw cannot pass by absence.
#
# Usage: scripts/skill-tier1-gate.sh <reports-dir> <skills-dir> [summary-file]
# Exits 0 when every gated validator passed, 1 when one did not, 2 when the
# reports are missing or malformed.
set -euo pipefail

reports=${1:?usage: skill-tier1-gate.sh <reports-dir> <skills-dir> [summary-file]}
skills=${2:?usage: skill-tier1-gate.sh <reports-dir> <skills-dir> [summary-file]}
summary=${3:-/dev/null}

# A run that did not produce reports to judge. As an annotation too, since
# the step summary and the log are not always at hand.
broken() {
  echo "skill-tier1-gate: $1" >&2
  echo "::error title=skill tier 1::$1"
}

ADVISORY='["Schema & Repository Governance","Security Scan"]'

if [ ! -f "$reports/summary.json" ]; then
  broken "no summary.json in $reports, so tier 1 did not finish."
  exit 2
fi

want=$(find "$skills" -mindepth 2 -maxdepth 2 -name SKILL.md | wc -l | tr -d ' ')
got=$(jq -r '.total // empty' "$reports/summary.json" 2>/dev/null || true)
if [ -z "$got" ] || [ "$got" != "$want" ]; then
  broken "the tree holds $want skills but tier 1 reported ${got:-none}."
  exit 2
fi

shopt -s nullglob
files=("$reports"/skill-*/skillevaluator-tier1.json)
if [ "${#files[@]}" -ne "$want" ]; then
  broken "$want skills but ${#files[@]} reports."
  exit 2
fi

failed=0
{
  echo "### Skills: SkillEvaluator tier 1 (ADR-0065)"
  echo
  echo "| skill | quality | gate | advisory |"
  echo "| --- | --- | --- | --- |"
} >> "$summary"

for file in "${files[@]}"; do
  if ! row=$(jq -e -r --argjson adv "$ADVISORY" '
      (.results // []) as $r
      | if ($r | length) == 0 then error("no results") else . end
      | {
          name: (.skills[0].name // "?"),
          score: ((.quality_summary[0].overall_score // "?") | tostring),
          bad: ([ $r[] | select((.validator | IN($adv[])) | not)
                       | select(.status != "passed")
                       | "\(.validator): \(.status)" ]
                # An advisory validator still has to have run: present,
                # with a verdict, and for Security Scan, the SkillSpector
                # report read back: its applicability block. A crash or a
                # missing report fails the gate (#1369).
                + [ $adv[] as $v | select([ $r[] | select(.validator == $v) ] | length == 0)
                    | "\($v): missing" ]
                + [ $r[] | select(.validator | IN($adv[]))
                         | select(.status | IN("passed", "failed", "incomplete") | not)
                         | "\(.validator): \(.status)" ]
                + [ $r[] | select(.validator == "Security Scan")
                         | select((.skillspector_applicability | type) != "object")
                         | "Security Scan: no SkillSpector report" ]),
          adv: [ $r[] | select(.validator | IN($adv[]))
                      | "\(.validator): \(.status)" ]
        }
      | [ .name, .score,
          (if (.bad | length) == 0 then "passed" else (.bad | join("; ")) end),
          (.adv | join("; ")) ]
      | @tsv' "$file" 2>/dev/null); then
    broken "$file is not a tier 1 report."
    exit 2
  fi
  IFS=$'\t' read -r name score gate adv <<<"$row"
  printf '| %s | %s | %s | %s |\n' "$name" "$score" "$gate" "$adv" >> "$summary"
  if [ "$gate" != passed ]; then
    echo "::error title=skill tier 1::$name: $gate"
    failed=1
  else
    echo "$name: passed (quality $score; advisory: $adv)"
  fi
done

advisory=$(jq -r --argjson adv "$ADVISORY" '
    (.skills[0].name // "?") as $n
    | .finding_groups[]?
    | select(any(.validators[]?; IN($adv[])))
    | select(.severity == "critical" or .severity == "high")
    | "- \($n): \(.check_name // .category): \(.message | split("\n")[0] | .[0:200])"' \
  "${files[@]}")
{
  echo
  echo "High and critical advisory findings, for the reviewer to read (not gated):"
  echo
  echo "${advisory:-none}"
} >> "$summary"

exit "$failed"
