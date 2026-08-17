# ADR-0016: Rebase-merge, atomic commits, and the review-fold policy

Date: 2026-08-17 · Status: accepted · Supersedes the merge-strategy clause of
ADR-0008

## Context

ADR-0008 chose squash-merge: the PR title became the one commit that landed.
The PR #7 review round surfaced the cost — the branch's commit story is
flattened, `main` carries one blob per PR, commit granularity means nothing,
and `git bisect` resolves only to PR boundaries. The maintainer wants clean,
bisectable multi-commit histories; the matching review-feedback policy already
runs in decorp and is proven there.

## Decision

- **Rebase & merge only.** The branch's commits land in `main` verbatim; linear
  history. Enforced by repository settings (squash and merge commits disabled).
  The PR title remains a valid Conventional Commit — it labels the PR and the
  tracker, though it no longer lands in history.
- **Atomic commits.** One logical change per commit — a subject that needs
  "and" means split. Each commit builds and passes test tiers 1–2 on its own,
  so history stays bisectable. Mechanical churn (gofmt, renames, file moves)
  never mixes with behavior changes. Every commit is a valid Conventional
  Commit — if no single type fits the diff, split it.
- **Review feedback folds into the origin commit.** No "address review"
  commits: fixes are folded into the commit that introduced the issue via
  autosquash rebase, proven by a byte-identical tree diff against a snapshot
  ref, pushed with `--force-with-lease` only. Genuinely new scope gets its own
  commit — or its own issue and PR. Procedure: `.claude/skills/review-fix/`.

## Consequences

- `git bisect` works commit-by-commit; the fold policy has a well-defined
  target; loops at L2 inherit an enforceable commit grammar.
- Accepted costs: a history rewrite marks inline review comments outdated and
  resets GitHub's changes-since-last-review diff.
- CI tests only the branch head, so mid-stack commit greenness is
  review-enforced; a per-commit CI job is deliberately deferred until bisect
  pain proves it worth the runtime.
- ADR-0008's PR-only, CI-green, and human-approval rules stand unchanged.
