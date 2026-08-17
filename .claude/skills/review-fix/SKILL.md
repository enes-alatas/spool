---
name: review-fix
description: >-
  Use when about to act on PR review comments on an active branch — before you start
  editing — or when asked to address review feedback, clean up a PR's commit story, or
  update a PR after a review round.
---

# /review-fix — fold review feedback into the original commits

The authority for _what_ to do with review feedback is **docs/CONVENTIONS.md** (§ Workflow,
ADR-0016): fold review-driven fixes into the commit that introduced the issue; do not add
follow-up "fix review comments" commits; rewrite branch history and update the same PR; keep a
separate commit only when the review introduced genuinely new scope. This skill is the
_procedure_ for applying that rule safely — do not restate the rule, follow it.

## Why this exists

The recurring failure is reaching for follow-up "address review" commits because they feel safer
than rewriting history. They are not — they litter the PR's story with corrections to commits that
should simply have been right. The rewrite below is mechanical and provable; this skill removes
the excuse.

## Procedure

1. **Read the whole review.** Inline comments live in a different place than the summary:
   - `gh pr view <n> --json reviews --jq '.reviews[] | "\(.author.login) [\(.state)]\n\(.body)\n"'`
   - `gh api repos/<owner>/<repo>/pulls/<n>/comments --jq '.[] | "\(.path):\(.line // .original_line)\n\(.body)\n[hunk]\n\(.diff_hunk)\n"'`
   - `gh api repos/<owner>/<repo>/issues/<n>/comments` for PR-level (non-inline) comments.

2. **Classify each comment — after checking it is right.** Reviewers are sometimes wrong: verify
   the comment's claim against the code first, and reply on the PR with your reasoning instead of
   implementing feedback that is incorrect. A comment phrased as a question gets a serious answer
   on the PR — do not read it as rhetorical unless that is unmistakable; it may need no code change
   at all. For the comments that hold: correction to existing
   code → _fold_ into the commit that introduced it; genuinely new scope → its own new commit
   (allowed by the policy). If a comment's intent is ambiguous (e.g. "this is unnecessary" on a
   method — the whole thing, or one line?), ask before acting; the wrong read can delete coverage.

3. **Map each fix to its origin commit.** For each fix, find the commit that introduced the lines
   it touches: `git log --oneline <base>..HEAD -- <path>` and `git log -S '<symbol>' <base>..HEAD`,
   where `<base>` is `$(git merge-base main HEAD)`. One fix can span several origin commits (a
   rename left half-done across a refactor commit and the feature commits built on it) — each part
   folds into its own origin commit. A confirmed issue is also rarely unique to the flagged line:
   sweep the branch diff for other instances of the same mistake and map each one as its own fix.

4. **Apply each fix as a fixup commit.** For each fix-part: make the edit, then
   `git add <files> && git commit --fixup=<origin-sha>`. A fix that spans several origin commits
   is applied part by part — edit part A, fixup onto its origin, edit part B, fixup onto its
   origin. New-scope commits (step 2) are also made now, as ordinary commits on top — the rebase
   leaves them in place. When everything is in, the tree already equals the final result: run the
   fast lanes once now (`make lint && make test && make itest`). Then snapshot:
   `git branch backup/<name>`.

5. **Fold everything with one rebase.** `git rebase --autosquash $(git merge-base main HEAD)` —
   since git 2.44 this works non-interactively, so the blocked `-i` flag is not needed. Original
   commit messages are preserved automatically. On a conflict, resolve toward what the commit
   being replayed should itself contain on top of the already-rebuilt commits — never by pasting
   in the final tree, which would smuggle later changes into earlier commits — then
   `git add <files>` and `GIT_EDITOR=true git rebase --continue`. A wrong resolution cannot slip
   through: step 6 catches it.

6. **Prove it.** `git diff backup/<name> --stat` must be **empty** — the tree is byte-identical,
   only redistributed across the right commits — and `git log --oneline <base>..HEAD` reads as the
   original story with no "fix review" commits and no leftover `fixup!` subjects (git before 2.44
   silently ignores `--autosquash` in a non-interactive rebase, so a leftover means the fold never
   ran). Do **not** re-run the fast lanes: an identical
   tree cannot produce different results, and step 4 already tested it. If the diff is not empty,
   recover with `git reset --hard backup/<name>` and retry (mid-rebase, `git rebase --abort`
   returns there too).

7. **Update the same PR.** `git push --force-with-lease`. The PR head moves automatically; the
   title, number, and base are untouched.

## GitHub gotchas (learned the hard way)

- **Never rename the PR's branch to fix a stale name.** GitHub's branch-rename **closes** the PR
  and it cannot be reopened against the vanished branch. A stale branch _name_ is harmless (it is
  not part of merged history); leave it, or close the PR and open a fresh one — do not rename in
  place.
- **Always `--force-with-lease`,** never bare `--force` — it aborts if the remote moved under you.
- **Inline review comments go "outdated"** after a history rewrite, because the commits they were
  anchored to no longer exist. That is the accepted cost of the policy; the comments stay readable
  in the conversation. Optionally reply to each noting the commit that now carries the fix.
- **Take the `backup/<name>` ref before the rebase, always.** It is the proof anchor in step 6 and
  the recovery path if anything goes wrong.
