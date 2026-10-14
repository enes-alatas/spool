# ADR-0054: A merge queue in CI, in place of "branches up to date"

Date: 2026-10-14 · Status: accepted (operator's decision of 2026-10-14 on #819) · Supersedes: ADR-0031 (the "require branches to be up to date" clause only) · Relates to: ADR-0016, #819

## Context

ADR-0031 turned on branch protection at the flip, with "require branches
to be up to date" on. That is what lets a push to `main` run tier 1 alone
(QUALITY.md): every PR has to be green against the current `main` before
it can merge.

With rebase-merge (ADR-0016) and five authors, that has a price on every
merge. The merge moves `main`, so every other approved PR is behind. The
reviewer asks its author to rebase, the author rebases, and CI runs
again, about ten minutes with `itest`, before the operator is asked to
merge. Two approved PRs take two of those round trips, and three take
three. On 2026-10-13 and 14 most merge asks waited on one.

GitHub's merge queue does exactly this job, but only for repositories an
organization owns. This one is owned by a user. Moving it to an
organization is possible, but it moves the Go module path, the release
images' namespace and the attestation identity (ADR-0052), so it is not
a step to take for this alone.

The operator asked whether CI could do what the queue does, and decided
on 2026-10-14 to build it.

## Decision

1. **A PR enters the queue with the `queue` label and auto-merge, both.**
   The reviewer adds the label with the approval; the operator turns on
   auto-merge. The operator is still the one who merges (ADR-0031):
   enabling auto-merge on a PR is that decision, taken ahead of the green.
   A labelled PR without auto-merge waits outside the queue, so the head of
   the queue is always a PR that merges the moment it is green.
2. **A push to a queued PR takes it out.** The decision in 1 is about the
   head that was reviewed. GitHub turns auto-merge off after a push only
   when the pusher cannot write to the repository, and every loop can. So
   the `unqueue` workflow, on every push to a same-repo PR, removes the
   `queue` label and turns auto-merge off, and the new head re-enters only
   through the reviewer and the operator again. A rebase to fix a conflict
   is a push like any other.
3. **The queue tests one PR at a time, on the `main` it would land on.**
   The `queue` workflow (`.github/workflows/queue.yml`) runs on every push
   to `main`, on a PR's label and auto-merge events, and by dispatch. It
   takes the queued PRs oldest number first, except that a PR already
   under test or green on the current `main` keeps the head. The head of
   the queue is rebased onto the current `main` inside the runner, with
   nothing pushed, which is the tree GitHub's rebase-merge would land.
   `ci.yml`'s gates run on that tree, called as a reusable workflow.
4. **The answer is a `queue` commit status on the PR's head, and it is a
   required check.** The status is green only for the head of the queue,
   and only if `main`, the PR's head, its label and its auto-merge are
   all still where the test found them. Every other queued PR is pending
   with its place, and an open PR that is not queued is pending "not
   queued". A PR that fails stays red, and the queue moves past it until
   its author pushes or it is queued again. When `main` moves, every
   queued PR goes back to pending and the new head is tested. A run
   cancelled mid-test leaves its head pending "testing"; the next push to
   `main`, PR event or dispatch of `queue` tests it again, and it keeps
   the head meanwhile.
5. **"Require branches to be up to date" goes off.** The `queue` check
   carries the guarantee that clause gave: a PR reaches `main` only after
   its gates passed against the `main` it lands on. `changes`, `checks` and
   `itest` stay required, so a PR's own CI still gates it as it does today.
   The ruleset should accept the `queue` status from GitHub Actions only.
6. **The job that runs a PR's code cannot write.** In the `queue` and
   `unqueue` workflows:
   - `test` runs the PR's code and holds `contents: read` and nothing else;
   - `pick` and `report` write statuses and run none of the PR's code,
     because they check out `main`'s copy of the script whichever event
     started them;
   - a PR's own events only mark the queue and dispatch a run on `main`,
     so the gates the head of the queue is tested under are `main`'s, which
     the PR cannot edit on its way in;
   - `unqueue` holds `contents: write`, which GitHub asks of a token that
     turns auto-merge off, with `pull-requests`, `statuses` and `actions`
     write. It runs `main`'s script, none of the PR's code, and removes the
     label first, so a refused auto-merge call leaves the PR out of the
     queue anyway, with the run red.
7. **A fork's PR is never queued.** A run started from `main` holds `main`'s
   cache scope and would carry the fork's code. A maintainer who wants a
   fork's change pushes its branch here first and queues that PR.
8. **Queueing a PR is trusting its code as merging it would.** The test
   runs in a run started from `main`, so the PR's code runs in `main`'s
   cache scope with a read-only token. A process on the runner can write
   to the Actions cache whatever the token says. So the label goes on a
   PR only once it is approved, Dependabot's included. That code is about
   to run on `main` anyway, in the tier-1 run its merge starts. What the
   queue adds is the case where it fails there and does not merge: it has
   still run once in `main`'s scope. A release never reads a cache
   (`release.yml`), so that scope does not reach a shipped artifact.

## Consequences

- **The rebase round trips end.** Approving a PR and turning on auto-merge
  is the last thing anyone does to it, unless it conflicts or fails on the
  newer `main`, and then its `queue` status says which.
- **The queue is serial.** Each merge costs one test of the next PR, about
  ten minutes when `itest` runs. GitHub's queue can test PR 2 on `main` plus
  PR 1 while PR 1 is still being tested. That speculative batching is left
  out until a queue long enough to need it shows up.
- **A queued PR's head must be green on its own CI too.** A head of the
  queue whose `queue` status is green but whose own `checks` are red does
  not merge, and holds the queue until its author fixes it or it is taken
  out.
- **`ci-health` reports the queue's machinery, not the PRs it turns red.**
  A `queue` run red only in `test` is a PR that conflicts with or fails on
  the newer `main`; its red `queue` status tells its author, and the pinned
  issue stays for broken CI. A red `pick`, `report` or `unqueue` is
  reported.
- **A fold after approval costs a second click.** The reviewer labels the
  new head again and the operator turns auto-merge on again. That is the
  price of decision 2, and it is the same review the head needed anyway.
- **What the queue widens is decision 8's, and only that.** A same-repo
  PR's author can push to this repository, so a workflow they edit could
  already write a status, and the `queue` status gives them nothing new.
  The one new thing is an approved PR's code running once in `main`'s cache
  scope before it merges. Decision 7 keeps forks out of it.
- **What the operator does once:** turn on "Allow auto-merge" in the
  repository settings, then in the `main` ruleset add `queue` as a required
  check, from GitHub Actions, and untick "Require branches to be up to date
  before merging". Until the second step, the queue posts its statuses and
  nothing depends on them.
