---
name: review-pr
description: >-
  Use when reviewing a pull request — a first read, a re-check after the
  author pushes, or deciding whether it is ready to merge.
metadata:
  author: Spool maintainers
---

# /review-pr — reviewing a pull request

A review answers one question: can this PR land as it stands, and if not,
what exactly must change. This skill is the procedure. The project's rules
(commit shape, what needs a test, a decision record or a doc update, who may
ask for the merge) live in its conventions; read them first and review
against them. This skill states the review policy; it doesn't set it.

## What to look for

Read the code against `code-review-and-quality` (`.claude/skills/`, vendored
verbatim): its five axes, its tests-first order, and its severity labels.
This skill is the procedure around that read: which rounds, which gates,
where the findings go, and when to approve. Where the two disagree, the end
of this skill says which wins.

## Why this exists

Each failure below was a real approval that should not have been. The diff
looked right every time; the defect was in the PR around it, in a file it
never opened, or in a claim nobody ran.

## 1. Gate on CI first

Read every check, not a filtered list: a required check that never ran
looks the same as a green one in a summary. List each row that isn't a pass
and account for it (skipped by design, pending, red). A red check gets a
request for changes before anything else. A failure that looks unrelated is
a flake only once you have shown it: the same tree passed elsewhere, or a
re-run passes, and you say which on the PR.

## 2. Review the PR, not just the diff

Re-read all of this on every round, not just the first:

- **The body.** Is the closing reference the right issue? Closing keywords
  ignore negation ("does not fix #12" closes #12), so read what the platform
  says the PR will close, not just the words. Does the evidence the body
  links (a run, a screenshot, a log) belong to the current head?
- **The issue.** Read its newest comments. A decision taken there, or in a
  comment, may already change what the PR should do.
- **The commits.** One logical change each, a subject that says what changed,
  and no "address review" commits if the project folds feedback into the
  commit it fixes.
- **The deletions.** On a refactor or a consolidation the defect hides in the
  minus lines: a guard, an ordering, an isolation or a dependency that the
  old code had and the new code silently drops. List the properties the
  removed code held and find each one in the new code.

## 3. Verify every claim yourself

An author's "fixed in all three places" or "this can't happen" is a claim.

- **Grep the unchanged tree.** When a PR renames something, flips a policy or
  corrects a premise, search the whole repository for the old name or the
  old sentence. Stale text hides in files the PR never touched, including
  comments that now describe something untrue.
- **Run it** when reading can't settle it. Check the head out in a
  throwaway worktree, never a checkout someone else is using, and ask what
  the run actually exercised: a green run that took an early exit proved
  nothing.
- **Open the screenshots and recordings.** Look at them; don't trust the
  description. If you can't open them, say so on the PR rather than passing
  them.
- **Read the code a claim depends on** before you assert a race, an ordering
  or a library behaviour. If you can't prove it, ask; don't block on it.
- **Cite only what you read.** Every file:line, ID and link in the review
  must come from something you opened in this review.

## 4. Write the review where the author works

- **Anchor each finding to its line** as an inline comment: what is wrong,
  the concrete case that breaks, and the rule or decision it violates.
- **The verdict body is a summary**: what you checked, the findings by
  severity, and one closing word. That word is either "approved at `<sha>`"
  or "changes needed". Never both, and never "approved with some fixes
  please".
- Label each finding's severity as `code-review-and-quality` does
  (**Critical:**, no prefix for required, **Optional:**, **Nit:**,
  **FYI**). The author can decline an optional one or a nit on the PR or
  fold it in, and either way you have the last word.
- Name the head SHA you reviewed. A new push voids the approval, and the
  re-check starts from the interdiff since that SHA.

## 5. Approve only what can land

Approve without reservation only when all of these hold:

- CI is green at the head you reviewed.
- The head can merge: on current main, or queued so it will be tested there.
  Check this from the platform's merge state, not by reading the base SHA.
- No operator decision is pending, and no required security pass is missing.
- Docs, tests and any decision record the change needs are in the same PR.

When everything holds except a decision only the operator can make, approve
it pending: say the code is done and name the open question. Don't send the
merge ask until the question is answered.

## 6. Send the merge ask with the approval

The reviewer asks for the merge, in the same message as the final approval,
with the PR link. The author never asks. Keep the chat message short, with
the substance on the PR. If the PR is still unmerged hours later, send one
short nudge with the link.

## Re-checking after a push

1. Diff the new head against the SHA you approved, not against main.
2. Read only that interdiff, plus the body and the thread again (step 2).
3. Re-run step 1, because the new head's CI is a new run.
4. Re-approve at the new SHA, or say what is still open.

## Where this differs from code-review-and-quality

- **Security has its own reviewer.** Note what you see on the security
  axis, but a PR that changes the security posture waits for the security
  reviewer's pass at the current head, and theirs is the verdict on it.
  Read their findings before you approve.
- **Size alone doesn't block.** Upstream says to split a change near 1000
  changed lines. Here a PR closes one scoped issue, and the commits are the
  unit of review: ask for a split when a commit mixes changes, or when the
  PR covers more than its issue, not for line count. This overrides only
  the changed-lines threshold: upstream's file-size signal still holds, so
  a file nearing 1000 lines is decomposed before it grows.
- **Approval has two halves.** Upstream approves a change that improves
  the code's health. That is the first half; steps 1 and 5 above are the
  second. Both must hold for the approval that carries the merge ask; an
  approval pending an operator decision says which half is still open.
- **Folding beats filing.** Upstream asks for a bug filed for cleanup the
  change can't hold. Do that only for work outside the PR's issue; a fix
  inside it folds into the commit it belongs to.

## In this repository

These rules from `docs/CONVENTIONS.md` apply; this section is what to strip
before publishing the skill elsewhere.

- Approve by a `--comment` review signed `— Name · Role`. The loops share
  one GitHub identity, so `--approve` fails, and no `@name` goes in a
  GitHub body.
- An approved PR gets the `queue` label, and the merge ask is "turn on
  auto-merge". Any push takes it out of the queue.
- A live `Operator decision pending:` line blocks the merge ask, and so does
  a `needs-security-review` label until the security reviewer has commented
  at the head.
- A `feat` touching only one of `internal/` and `web/` names its other half;
  engine behaviour needs a tier-2 test in `itest/`; prompts live only in
  `internal/loop/prompt.go`; an accepted ADR changes only per `/amend-adr`.
