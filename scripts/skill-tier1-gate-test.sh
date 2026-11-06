#!/usr/bin/env bash
# Exercises scripts/skill-tier1-gate.sh against synthetic tier 1 reports
# shaped like SkillEvaluator 0.8.0's (the image ADR-0065 pins).
#
# The cases are the ones #1369's probe found plus the ways a gate goes
# quietly green: a scanner that could not run, a validator the next image
# adds, a skill the evaluator never reported.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# A skills tree with n skills, and an empty reports dir beside it.
setup() {
  rm -rf "$work/skills" "$work/reports"
  mkdir -p "$work/reports"
  for i in $(seq 1 "$1"); do
    mkdir -p "$work/skills/s$i"
    printf -- '---\nname: s%s\n---\n' "$i" > "$work/skills/s$i/SKILL.md"
  done
  jq -n --argjson t "$1" '{total: $t}' > "$work/reports/summary.json"
}

# report <n> <validator=status>...: every validator SkillEvaluator 0.8.0
# runs, passed, then the overrides.
report() {
  local n=$1; shift
  mkdir -p "$work/reports/skill-00$n"
  local overrides='{}' kv
  for kv in "$@"; do
    overrides=$(jq -c --arg k "${kv%%=*}" --arg v "${kv#*=}" '. + {($k): $v}' <<<"$overrides")
  done
  jq -n --arg name "s$n" --argjson o "$overrides" '
    ["Schema & Repository Governance","Semantic Version Validation","Security Scan",
     "PII Scan","License Compliance","Code Risk Analysis","Secrets Detection",
     "Code Integrity & Hygiene","Dependency Vulnerability Audit",
     "Unicode Smuggling Detection","QUALITY","SCRIPT_LINT"] as $all
    | ($all + ([$o | keys[]] - $all)) as $names
    | {
        skills: [{name: $name}],
        quality_summary: [{overall_score: 85.5}],
        results: [ $names[] | {validator: ., status: ($o[.] // "passed")}
                   # "nospector": SkillSpector wrote no report; "absent": not run at all.
                   | if .validator == "Security Scan" and .status != "nospector"
                     then . + {skillspector_applicability: {}} else . end
                   | if .status == "nospector" then .status = "incomplete" else . end
                   | select(.status != "absent") ],
        finding_groups: [
          {category: "SECURITY", severity: "high", check_name: "Tool Parameter Abuse (TM1)",
           message: "Tool Misuse: example finding", validators: ["Security Scan"]}
        ]
      }' > "$work/reports/skill-00$n/skillevaluator-tier1.json"
}

# name, wanted exit code
run() {
  local name=$1 want=$2 out status
  out=$(bash "$root/scripts/skill-tier1-gate.sh" "$work/reports" "$work/skills" "$work/summary.md" 2>&1)
  status=$?
  if [ "$status" -eq "$want" ]; then pass "$name"; else fail "$name: want exit $want, got $status: $out"; fi
}

setup 2; report 1; report 2
run all-passed 0

# What every skill here looked like in #1369's probe: the advisory two not
# passing, everything else passing. This is the case the gate exists for.
setup 2
report 1 "Schema & Repository Governance=failed" "Security Scan=incomplete"
report 2 "Schema & Repository Governance=failed" "Security Scan=failed"
run probe-shape-passes 0
if grep -qF 'Tool Misuse: example finding' "$work/summary.md"; then
  pass "advisory findings reach the summary"
else
  fail "advisory findings reach the summary: $(cat "$work/summary.md")"
fi

setup 1; report 1 "Secrets Detection=failed"
run secret-found 1

setup 1; report 1 "QUALITY=failed"
run below-quality-floor 1

setup 1; report 1 "Unicode Smuggling Detection=failed"
run unicode-smuggling 1

# A scanner that could not run is red, not a pass by default.
setup 1; report 1 "Secrets Detection=incomplete"
run gated-scanner-incomplete 1
setup 1; report 1 "PII Scan=skipped"
run gated-scanner-skipped 1

# The next image adds a validator: it gates until an ADR says otherwise.
setup 1; report 1 "A Validator Not Yet Invented=failed"
run new-validator-gates 1

# Advisory is not optional: SkillSpector that crashed or wrote nothing, a
# verdict that isn't one, or an advisory validator missing, all fail.
setup 1; report 1 "Security Scan=nospector"
run skillspector-wrote-no-report 1
setup 1; report 1 "Security Scan=error"
run security-scan-errored 1
setup 1; report 1 "Schema & Repository Governance=skipped"
run schema-skipped 1
setup 1; report 1 "Security Scan=absent"
run security-scan-missing 1

# The tree holds two skills; the evaluator reported one.
setup 2; report 1
jq -n '{total: 1}' > "$work/reports/summary.json"
run skill-not-reported 2
setup 2; report 1
run summary-claims-more-than-reports 2

setup 1; rm -f "$work/reports/summary.json"
run no-summary 2

setup 1; mkdir -p "$work/reports/skill-001"; echo '{}' > "$work/reports/skill-001/skillevaluator-tier1.json"
run empty-report 2

setup 1; mkdir -p "$work/reports/skill-001"; echo 'not json' > "$work/reports/skill-001/skillevaluator-tier1.json"
run malformed-report 2

if [ "$failures" -ne 0 ]; then
  printf '%d case(s) failed\n' "$failures"
  exit 1
fi
echo "all cases passed"
