# ADR-0014: Mandatory issue typing via forms and labels

Date: 2026-08-17 · Status: accepted

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
3. **CI** (`needs-type` workflow on the `issues` trigger) labels any issue
   without a type label `needs-type` and clears the flag once typed.

`bug` and `security` severity is mirrored as `severity:critical|high|medium|
low|info` labels. Issue types map onto Conventional Commit / branch types
(bug→`fix/`, new feature→`feat/`, documentation→`docs/`, …), so an issue's type
names the branch that closes it. PRs use `.github/pull_request_template.md`
(Issue link with `Closes`/`Part of`, Summary, ADRs, Verification, Commit
Structure, Notes/Risks).

## Consequences

- Loops at L2 get a filing contract that is machine-followable and
  review-checkable; untyped strays surface automatically instead of silently.
- Template texts are duplicated from agenttap/decorp rather than shared; drift
  between repos is accepted (Spool's copies are canonical for Spool).
- If Spool moves to a GitHub organization, native issue types can supersede the
  label mechanism (new ADR).
