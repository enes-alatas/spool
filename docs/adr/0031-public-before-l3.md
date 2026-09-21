# ADR-0031: The repo goes public when the go-public checklist is done, not at L3

Date: 2026-09-21 · Status: accepted · Supersedes: ADR-0007 (timing clause only)

## Context

ADR-0007 decided that the repo stays private through L0–L2 and goes public when
the Slack-coworker story demos well at L3. That put publication behind a feature
milestone. A year of fleet operation later, three things that have nothing to do
with Slack are waiting on the flip:

- **Actions minutes.** The fleet's own PR traffic is the load, and it bills at
  roughly three times what the plan includes — after the efficiency work in #232
  cut it as far as job-count and trigger design can. Public repositories run
  Actions free. On 2026-09-21 the account ran out mid-day and every job stopped
  starting, which is the shape this problem takes: not a bill, a halt.
- **Secret scanning and push protection.** Free on public repositories, paid on
  private ones. The repo's own rules (CONVENTIONS.md) already say no credential
  reaches a commit; scanning is the mechanism behind the rule rather than a
  restatement of it.
- **Branch protection on `main` (#1).** Private-plan limits have kept it off,
  so nothing stops a push straight to `main`, and the checks a PR runs are
  advisory. "Never merge your own work" stays a rule loops keep either way —
  see the consequence below for what protection can and cannot enforce for a
  single-account fleet.

The operator decided on 2026-09-20 that none of these should wait for a demo.
This reverses a decision rather than narrowing one, so per the amendment rule
(#188) it is a new ADR rather than an edit to ADR-0007.

## Decision

The repository becomes public as soon as the go-public checklist (#153) has
cleared, rather than at L3. #153 is the gate: history audit, secret sweep,
`SECURITY.md`, license and headers, and the settings the flip needs. Its blocking
items cleared on 2026-09-21 and the flip was performed the same day; the issue
itself stays open for what is left (see Consequences).

L3 keeps its scope and stops carrying the flip. It is when the project is
*announced* — the demo a stranger is pointed at — which is a different event
from the repository being readable.

## Consequences

- **The board is world-readable, including its history.** The existing rules
  hold and gain one: nothing said in the Telegram group is quoted on GitHub.
  The group is where the fleet coordinates and the operator thinks out loud;
  an issue that needs the substance of a group message states it in its own
  words. The rules it joins: no `@mentions` in any GitHub artifact (they ping
  strangers), sign what you write, and never put owner-DM content anywhere but
  the DM.
- **A fork PR is not a trusted run.** Workflows triggered by a pull request
  from a fork get a read-only `GITHUB_TOKEN` and no access to secrets, so
  `issue-guards` and `ci-health` cannot act on one — they comment, and a
  read-only token cannot. PR CI itself runs and gates normally. QUALITY.md says
  which of the gates that leaves standing.
- **Branch protection goes on at the flip** (#1), with `changes`, `checks` and
  `itest` as the required checks — the job names #232 left behind — and
  "require branches to be up to date" on, which is what lets a push to `main`
  run tier 1 alone (QUALITY.md). What that buys is precise: a change reaches
  `main` only through a pull request, only by rebase, and only with those three
  checks green against current `main`. It does **not** buy an approval. The
  ruleset sets `required_approving_review_count: 0`, and it has to: GitHub does
  not let an account approve its own pull request, and this fleet is one
  account, so any higher number would make every PR unmergeable. Review stays
  a rule the fleet keeps and the operator enforces by being the one who merges
  — protection makes the door narrow, not guarded.
- **`SECURITY.md` is the disclosure route** from the moment the repo is
  readable: a stranger who finds something needs somewhere to send it that is
  not a public issue.
- **The README says what a reader is looking at**: pre-1.0, developed in the
  open by a fleet of Claude Code loops and their operator. A repo whose commits
  are co-authored by a model and whose issues are filed by four agents is
  otherwise a puzzle before it is a product.
- **The flip happened on 2026-09-21, before this ADR merged.** Recorded here so
  the sequence is not archaeology: the repo went public at ~13:38 UTC and the
  `main` ruleset became active at 13:40, the same day the Actions quota ran out.
  What was load-bearing in #153 and done first: the history and body audit, the
  orphaned pre-fold commits, the attachment eye-check, `SECURITY.md`, and the
  flip-day settings — secret scanning, push protection, private vulnerability
  reporting, read-only workflow token, fork-PR approval. #153 stays open for the
  residue: eleven de-referenced attachments that answer 404 anonymously but are
  still stored, and the retrospective edits their removal implies. The gate this
  ADR names is the checklist's blocking items, not the issue being closed.
- ADR-0007's other consequences are untouched and still hold: every commit is
  written to be published, clean history, no secrets in the repo, license and
  headers at L0.
