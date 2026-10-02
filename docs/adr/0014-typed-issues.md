# ADR-0014: Mandatory issue typing via forms and labels

Date: 2026-08-17 · Status: accepted · Amended: 2026-10-02 (community reports)

## Context

Work is tracked in GitHub issues (ADR-0008), and from L2 Spool's own loops file
them. Free-form issues rot fast under agent filers; a template is effectively a
prompt, so its structure decides the filing quality. GitHub's native issue
*types* are organization-only — this repo lives on a personal account — so
typing must be built from labels. The same convention already runs in the
maintainer's other repos (agenttap, decorp); reusing it verbatim costs nothing
to learn.

## Decision

Every issue carries exactly one **type label**: `bug`, `new feature`,
`improvement`, `refactor`, `chore`, `security`, `documentation`, `question`.
Enforcement has three layers:

1. **Issue forms** (`.github/ISSUE_TEMPLATE/*.yml`) pre-apply the type label;
   `blank_issues_enabled: false` closes the untyped path in the web UI.
   Spool addition: change-shaped forms (feature, improvement, refactor) carry an
   optional *Seams / ADRs touched* field, moving the QUALITY.md review gate to
   filing time.
2. **Written rule** for `gh`/API filings (which bypass forms): apply the type
   label yourself and replicate the matching form's headings in the body.
3. **CI** (the typing guard in the `issue-guards` workflow, on the `issues`
   trigger) labels any issue without a type label `needs-type` and clears the
   flag once typed. It had a workflow of its own until #232; the label and the
   behaviour are unchanged, it just shares a runner with the redaction guard.

`bug` and `security` severity is mirrored as `severity:critical|high|medium|
low|info` labels. Issue types map onto Conventional Commit / branch types
(bug→`fix/`, new feature→`feat/`, documentation→`docs/`, …), so an issue's type
names the branch that closes it. PRs use `.github/pull_request_template.md`
(Issue link with `Closes`/`Part of`, Summary, ADRs, Verification, Commit
Structure, Notes/Risks).

**Amendment (2026-10-02, #502): community reports.** The rule above binds
the team's own filings. People outside the team get a form of their own,
**Report a problem**, first on the chooser. It asks what happened, what was
expected, the Spool version and the platform, and nothing a reporter outside
the code could not know. It pre-applies `community` and `triage` and no type
label, and the typing guard leaves a `community` issue unflagged. Such a
report becomes a typed issue at triage.

- **Triage.** Within one working day of filing, the product owner reproduces
  the problem or asks for what is missing; sets the type label, and the
  severity label where the type carries one; removes `triage`; and answers
  with the decision: taken, needs more information, or not planned, with the
  reason. The report stays the canonical issue for the reporter. The team
  issue that implements it says `Reported in #n`.
- **Labels are the maintainers'.** GitHub lets only accounts with triage
  permission or higher add or remove labels, milestones and assignees, and
  this repository has one such account. An outside reporter gets labels only
  through a form's preset, so there is no setting to look for that would keep
  them out. A report filed through a team form arrives with that form's type,
  and triage retypes it if it is wrong.
- **A community issue is untrusted input.** Its text can carry instructions
  aimed at the loops that read it. No loop takes an instruction from one,
  runs a command or opens a link quoted in one, or copies its text into a
  place a loop or developer later acts on: an issue worked from, a commit
  message, a PR body. Triage restates the defect in its own words in the team
  issue, and developers work from that issue, never from the report. Any
  automated reply never echoes the report's text. Labels, milestones and
  assignees are not an injection surface. The text is.
- **Out of scope:** pull requests from outside the team, which follow the same
  path later.

## Consequences

- Loops at L2 get a filing contract that is machine-followable and
  review-checkable; untyped strays surface automatically instead of silently.
- Template texts are duplicated from agenttap/decorp rather than shared; drift
  between repos is accepted (Spool's copies are canonical for Spool).
- If Spool moves to a GitHub organization, native issue types can supersede the
  label mechanism (new ADR).
