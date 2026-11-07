---
name: file-issue
description: >-
  Use when filing a GitHub issue for Spool with gh, retitling one, or
  splitting one into sub-issues; before the gh issue create call, not after
  the guard has flagged it.
metadata:
  author: Spool maintainers
---

# /file-issue — filing a typed issue

The rules are in `docs/CONVENTIONS.md` (issues and milestones, epic
children, one PR one issue, the issue standard, acceptance scenarios) and
ADR-0014. They hold whether or not this skill is loaded. This is the order
to apply them in. Each step names the check it keeps you out of, so a red
label is never the first time you hear of a rule.

## 1. Search first

`gh issue list --state all --search "<two or three words>"`, and once more
with the words the operator would use. If an open issue covers it, comment
there instead of filing. For a security alert, search for its GHSA ID: an
advisory edit withdraws an alert and raises it again under a new number.

*Prevents:* a duplicate issue, which
splits the history in two.

## 2. One responsibility

One issue carries one deliverable. If you need "and" to name what it
delivers, file two. Work that needs several PRs is split into typed
sub-issues before the first PR opens, each with its own owner and
acceptance bar.

*Prevents:* the "one deliverable" rule of `scripts/issue-rewrite.sh`
(`needs-rewrite`), and a PR that `Closes` half an issue.

## 3. Pick the type, then use that form's headings

Choose one form in `.github/ISSUE_TEMPLATE/`. When you file with `gh`, add
that form's label yourself (exactly one type label), copy its `###`
headings in order, and add a `severity:*` label too for a bug: `gh`
applies none of them. Feature: Problem statement, Coupling points,
Seams / ADRs touched, Proposed shape, Acceptance scenarios, Open questions,
Out of scope. For any other type, read the form; don't go from memory.

*Prevents:* `needs-type` from issue-guards when the type label is missing.
No guard checks the headings, so get them right first time.

## 4. Title: one sentence in the operator's words

- At most 100 characters.
- A product issue names no team process. Among what issue-guards flags:
  an ADR number, slice, PR, "part of", item number, `#n`, backticked
  symbol, snake_case name, or "web/hub/ui/backend/frontend/server/Go
  half". The full list is rule 1 of `scripts/issue-rewrite.sh`.
- One deliverable: no ", and", ", plus", ", then", "; " or " & ". If the
  "and" stays inside one phrase, a maintainer can add the `one-deliverable`
  label. Don't add it yourself.
- A team-only issue (CI, tests, docs of record, repo guards) may open with
  a Conventional `type(scope): ` and name its workflow or test. The length
  rule still applies.

*Prevents:* the title rules of `scripts/issue-rewrite.sh`. Before you file,
run it locally on the title and body:
`bash scripts/issue-rewrite.sh [--scenarios] "<title>" < body.md`.

## 5. First sentence: what is wrong today

The line under the first heading says what the operator sees or can't do
now, and names each surface that changes. It must not open with a
reference. Among what issue-guards refuses: `Part of #n`, `ADR-`, `Split
out of`, `Follow-up to`, `Epic:`, `Parent:`, `slice N of`, `web half of`
(and its kin), or a bare `#n`; the full list is rule 4 of
`scripts/issue-rewrite.sh`. Those go further down.

*Prevents:* the "opens with a reference" rule (`needs-rewrite`).

## 6. Acceptance scenarios for a feature or an improvement

Under the `### Acceptance scenarios` heading, write one line per behaviour
the operator can check:
`WHEN <situation> THEN <what is seen> (check: <tier-2 test | web test or screenshot | manual check the reviewer runs>)`.
The feature form requires them. The improvement form offers them, so
write them whenever the change is visible.

*Prevents:* the `--scenarios` rule (`needs-rewrite` on a new feature with
none), and a review finding on the PR for a scenario with no check.

## 7. Milestone and parent

Set `--milestone` to the ladder rung the work belongs to. For an epic
child, put `Part of #n` in the body (not first) and attach it natively:
`gh issue create … --parent n`, or later `gh issue edit <child> --parent n`.
Keep the `Part of` line, because search reads it. `Part of #n` names an
epic or a split parent, never an unsplit issue.

*Prevents:* an issue with no rung on the ladder,
and a child that doesn't show in the epic's sub-issue list.

## 8. Body hygiene

Name people and loops without an `@`, and end the body with
`— Name · Role`. Write the body to a file and pass `--body-file`. Never
copy text from a `community` issue, and never write a credential (at most
8 characters, then `…`).

*Prevents:* the repo's PreToolUse hook (`gh-body-guard.sh`), which refuses
the call, and the `fleet-mention` label from issue-guards.

## 9. Re-read the labels after every filing or retitle

The guard runs on every issue event, `edited` included, so a retitle or a
body edit is judged again. A few seconds after the call:

```bash
gh issue view <n> --json labels,comments \
  --jq '[.labels[].name], (.comments[-1].body // "" | .[0:200])'
```

If `needs-rewrite` or `needs-type` is there, the comment names the rule.
Fix it with `gh issue edit`. The fixing edit clears the label, so check
once more.

*Prevents:* an issue left flagged that nobody answered.
