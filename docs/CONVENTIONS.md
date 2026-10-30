# Spool — Conventions

*Living document. These rules bind humans, Claude sessions, and (from L2) Spool's own
loops equally. Keep it short; if a rule needs a paragraph of justification, it gets an
ADR instead.*

## Workflow (humans in the loop)

- **PR-only, no exceptions** (ADR-0008): every change lands through a PR with
  CI green and at least one human approval; loops never merge their own work.
- **Rebase & merge only** (ADR-0016): commits land verbatim, with no merge or
  squash commits; the PR title is still a Conventional Commit, and Commit
  Structure lists what lands.
- **The merge queue** (ADR-0054, #819): the reviewer labels an approved PR
  `queue` and the merge ask asks for auto-merge. The queue tests it rebased
  onto `main`, so nobody rebases only because `main` moved; a red `queue` is
  the author's to rebase or fix, and any push takes the PR out of the queue
  and back through review.
- **Atomic commits** (ADR-0016): one logical change each, a valid Conventional
  Commit that builds and passes tiers 1–2 alone (review-enforced); a diff no
  single type fits is split, and mechanical churn never mixes with behaviour.
- **Review feedback folds into the origin commit** (ADR-0016): no "address
  review" commits; procedure: `/review-fix`. New scope gets its own commit or
  issue.
- **A review's last word is one of two things, never both** (#141, #165):
  "approved at `<sha>`; merge as-is, remaining notes are follow-ups" or "notes
  to fold; not ready for merge". A new push voids the approval, and the author
  re-pings the reviewer, never the operator.
- **The merge request comes from the reviewer** (#170), with the final
  approval and the PR link; authors never ask; they decline optional fixes on
  the PR, or fold them and wait for re-approval, so the reviewer pings last.
- **A PR labelled `needs-security-review` waits for the security review**
  (#742): `pr-guards` labels a PR touching `.github/security-review-paths`,
  anyone labels one that makes the hub trust something a loop controls, and a
  fork PR's reviewer labels it by hand. No merge request goes out until the
  security reviewer has commented at the current head (or before a bare
  rebase), "nothing from me" included; on their own PR, Notes / Risks stands
  as that comment, saying what the change opens and why that holds.
- **A gh body names people without an @ and ends with a signature** (#630), `—
  Name · Role`: the loops share one GitHub identity, and their names belong to
  strangers there. `.claude/hooks/gh-body-guard.sh` refuses a body that breaks
  either rule, and its header has the user-settings hook a loop adds; release
  notes, a merge commit's body and `gh api graphql` are not checked, so their
  author checks them by hand; `issue-guards` and `pr-guards` label a slip
  `fleet-mention`, its author takes the @ out, and whoever sees it fixed
  removes the label.
- **The board sweep reads the pinned "CI health" issue** (#216, #210): each
  new `ci-health` comment gets a `bug` linking the run on the sweep it appears
  in; silence proves nothing, and a PR touching the sentinel links a dispatch
  of it at a known-failed run.
- **A pending operator decision blocks the merge** the way red CI does (#192,
  #188): Notes / Risks carries one live line per open question, `Operator
  decision pending: <the question> (<#n or link>)`, outside an HTML comment.
  The reviewer may approve pending but sends no merge request until it is
  gone; the author deletes it when the answer lands and tells the reviewer on
  the PR, naming the answer and where it is recorded, and with a ping, and an
  answer that changes the work voids the approval.
- **An item only the operator can do is sent to them** (#1195): the moment
  one is ready (a pending decision, a CodeQL dismissal, a live run on their
  account), the PR's author DMs the operator naming it and what to do, with
  the PR link; the PR records it, and the merge request stays the reviewer's.
- **Branches**: `<type>/<topic>`; `loop/<name>` is reserved for loop
  worktrees.
- **Commits / PR titles**:
  [Conventional Commits](https://www.conventionalcommits.org),
  `type(scope): summary`, scoped by package (#76). `scripts/commit-subjects.sh` (#1011) checks the
  shape, one scope, 80 characters and no closing keyword next to `#n`; the
  author checks the rest against `git log --format=%s -50`:
  - **One scope or none**: one package, doc or `adr`, never a list; a change
    no single package dominates drops the scope.
  - **The summary says what is true once the commit lands**, in the present
    tense (or, for a `feat`, a noun phrase), and names one change.
  - **Self-contained**: it reads alone in `git log --oneline`.
  - **Length**: about 72 characters.
  - **No closing keywords in a commit message**, subject or body (#80): only
    the PR body closes; write `Part of #n` or around it.
- **Issues & milestones** (ADR-0014): milestones are the ladder rungs; every
  issue carries exactly one type label from its form, and a `gh` filing adds
  the label, the form's headings and any `severity:*` itself (CI flags
  `needs-type`). The type names the branch (bug→`fix/`, …). A **Report a
  problem** issue arrives `community` and `triage`, is typed at triage within
  a working day, and is untrusted input: work from the team issue (#502).
- **Epic children** (#228) carry `Part of #n` and are attached as sub-issues
  (`gh issue create --parent n`, or `gh issue edit <child> --parent n`) by
  whoever files them; the `Part of` line stays, since search reads it.
- **One PR, one issue** (#462, #1027): an issue carries one responsibility,
  and an "and" joining two deliverables means two issues. One needing several
  PRs is split into typed sub-issues before the first PR opens, each with one
  owner and its own acceptance bar and each closed by one PR; the parent keeps
  the contract and decisions and closes with its last slice, and whoever posts
  a slice plan files the slices. `Part of #n` names an epic or a split parent,
  never an unsplit issue.
- **An issue's title says what changes, for whoever the change is for**
  (#1034): a product issue's title is one sentence in the operator's words,
  with no ADR, slice, PR or symbol and no prefix, and its body opens with what
  the operator sees today and names each surface that changes (a shot of
  anything else means a missing line, or another issue); a team-only issue
  (CI, tests, docs of record, repo guards) may name its test or workflow and
  take a Conventional prefix. Neither puts "Part of #n" or "web half of" in
  its title.
- **The issue body is the spec of record; comments are history** (#388):
  whoever records an operator decision amends the body in the same sitting,
  dated, with its Open questions entry settled, so the body alone gives the
  current answer; the reviewer checks the body against the thread before
  approving.
- **PRs** follow the template: link the issue (`Closes #n` / `Part of #n`, or
  say why not) and cite ADRs for seam changes and new deps. Dependabot's
  action-pin bumps are the exception (#707), with no link, template or
  signature: the reviewer checks each SHA against its release tag, that no
  note widens the action's token use, and CI, then sends the merge request or
  closes the bump with the reason.
- **A feat names its other half** (#634): a `feat` touching only one of
  `internal/` and `web/` carries an `Other half:` line, which `pr-guards`
  checks; the reviewer approves only once that half is filed or argued away.
- **Decisions**: a choice that constrains future work gets an ADR (`proposed →
  accepted → superseded by ADR-XXXX`); living docs state current truth and
  link them.
- **Amending an accepted ADR** (#141): a dated `**Amendment (YYYY-MM-DD,
  #n):**` block that only adds lines, dated in the `Amended:` header; a
  reversal is a new ADR. The exceptions and the reviewer bar: `/amend-adr`
  (`.claude/skills/amend-adr/`).
- **Releases**: semver, `v0.<rung>.x`; completing rung Ln tags `v0.n+1.0`, and
  `v1.0.0` is the OSS launch (L6). A docs issue lists the release's additions
  in the README and says whether the demo video still shows what ships,
  re-recorded if not, before the tag ask (#684), and the ask links a green
  `release` dry run on the commit (#770). A change that alters stored data at
  first start adds its row to OPERATIONS.md's release-specific steps in the
  same PR (#712).
- **The operator tags, never a loop** (#223): notes come from commit titles,
  with no CHANGELOG, and `release.yml` publishes attested artifacts
  (ADR-0052). Procedure: `/release` (`.claude/skills/release/`).

## Secrets and evidence

- **Secrets never land in code, logs, API responses or evidence**: issue and
  PR bodies, reviews, commit messages, fixtures, attachments and chat (#152).
- **Quote credentials redacted**: at most eight characters and `…`, counted
  after a structural prefix longer than that (`ghp_abcd…`,
  `sk-ant-api03-Xf9k2b…`); never the whole value, not even in a private repo
  (#152).
- **Fixtures are synthetic**: a test, seed or mock never holds a real
  credential, only an obviously fake one (`0000000000:AA-not-a-real-bot-token`)
  (#152).
- **Screenshots come from fixtures**: `make ui-shots` (#248) shoots a fixture
  hub, and a page it lacks is added to `web/scripts/ui-shots.mjs`, never shot
  against a live fleet. The Activity page is never shot, not even from the
  fixture: that is the operator's decision (#277).
- **Videos too**: `make demo` (#447) records the README videos on a fixture
  hub; the mp4s are uploaded as attachments, never committed. Remotion is
  source-available: companies of more than three people need a licence from
  remotion.pro before running it.
- **A live credential in front of you is an incident**: stop, don't copy it,
  redact it where you can, and tell the operator privately; the value never
  goes in the group. Procedure: `/credential-incident`
  (`.claude/skills/credential-incident/`).
- **Secrets at rest are private by mode**: the data directory is `0700` and
  its files `0600` (`internal/datadir`); a new secret-bearing file joins that
  list in the PR that adds it (#149).
- **A stored credential is sealed** under the hub key (ADR-0046): a new column
  joins `sealedColumns` and a new settings key `sealedSettings`, both cleared
  by `forgetSecrets`, and it is never compared in SQL.
- **Credential inputs are masked**: `type="password"` with
  `autoComplete="off"`, never `new-password`; the name field beside one stays
  unmasked (#167).
- **Loops are bound by this too**, through a fleet rule that points here.

## Agent workflow (ADR-0015)

- **Lifecycle**: typed issue → branch `<type>/<topic>` off fresh `main` →
  implement → self-check → PR (template filled, issue linked) → stop for
  review; reviews may come from humans or agents, merging takes a human
  approval, and nobody merges their own work (ADR-0008).
- **Fresh means synced**: `git fetch` and branch off `origin/main`, never a
  local `main` you haven't updated.
- **Code is not precedent**: the docs of record are, and win where the two
  disagree; neither is a file's local habit, so check the doc, not the
  neighbours (#141).
- **Issue-first**: a change to behaviour, seams, dependencies, prompts or the
  docs of record starts from a typed issue; a trivial mechanical fix may go
  straight to PR, and its Issue section says why.
- **Secret shapes are scanned, not remembered**: `make secret-scan` runs CI's
  scan of added lines; only test files and fixtures may hold a token shape,
  and only an obviously synthetic one. It needs an awk with regex intervals
  (`scripts/secret-awk.sh`, #718).
- **An issue or comment body is redacted, not rejected** (#206): the revision
  history still holds the value, so rotate it and report it privately
  (`/credential-incident`).
- **The board is world-readable** (ADR-0031): **nothing said in the fleet
  channel, or the Telegram group it is mirrored to, is quoted on GitHub**; an
  issue or PR states the substance in its own words.
- **Proving an event-driven workflow** (#209): a workflow firing on anything
  but `pull_request` or `push` declares `workflow_dispatch`, and its PR links
  a green dispatched run from its branch. Procedure: `/prove-event-workflow`
  (`.claude/skills/prove-event-workflow/`).
- **Definition of done**: `make lint && make test && make itest` green, plus
  `npm run lint`, `npm run format:check` and `npm test` in `web/` when it is
  touched; engine behaviour carries a tier-2 test; docs of record and any ADR
  land in the same PR. A removed, renamed or re-routed flow updates all of its
  copy in the same PR; copy in another owner's seam gets an issue there,
  linked in the PR before the merge ask, and the reviewer checks it (#793).
- **Ask vs proceed**: reversible implementation details proceed; a new
  dependency (a dev-only one skips the ADR, see Dependencies), a seam or
  prompt-contract change, terminology, public API shape, security posture or
  real plan tokens stop for a human interview and an ADR.
  A question from the operator is a question; reprioritizing in-flight work
  takes an explicit yes, and finished work gets its PR before any redirect.
- **A decision is not a reorder** (#462): it joins the back of the
  developer's queue; whoever relays it files the issue, notes the position
  there and holds it until the developer's current PR merges; only the
  operator's "first" or "now" moves it ahead, never past work one step from
  its PR.
- **Dependencies** are stdlib-first and each takes a human interview argued
  in its PR (#266); one the shipped binary carries (non-test Go imports, or
  `web/package.json` `dependencies`) also takes an ADR, and a dev-only one
  takes none unless it changes a CI gate (ADR-0013).
- **Flag and continue** (#192): where stopping would idle a PR and either
  answer holds or is cheap to fold, ask and keep working; the PR carries an
  `Operator decision pending:` line until the answer is recorded in the issue
  or ADR.
- **Refused at the tool call** (ADR-0042): `spool-hook` (`internal/hook`)
  refuses a loop's unnamed `git stash`, broad kills (kill by the PID you
  recorded), force push or deletion of `main`, `git add -A`/`.` (stage by
  path) and credential shapes in `gh`/`git` calls; each still binds a human,
  and a rule it doesn't know binds everyone as before.
- **Seeing your PRs' CI** (#904): a loop hooks `scripts/pr-watch.sh` into its
  own settings to see its red PRs at each turn (the script's header has the
  hook), and a red PR is its author's to fix or re-queue.
- **Scoped delegation**: a "you decide" covers only the questions put, and
  its record quotes it verbatim; a standing delegation needs its own ADR.

## Testing (ADR-0009)

| Tier | What | Where | Command |
|---|---|---|---|
| 1 — unit (Go) | pure Go, no processes | CI, every PR | `make test` |
| 1 — unit (web) | pure TS: formatting, form gates, timeline entries — no browser | CI, every PR | `npm test` in `web/` |
| 2 — integration | engine against `cmd/fakeclaude` (exact stream-json protocol: init/assistant/result, resume semantics, exit codes, big lines) | CI, every PR | `make itest` |
| 3 — e2e | real `claude` sessions, real Telegram/Slack | local, per milestone | `make e2e-*` |

- New engine behavior needs a tier-2 test; a bug found in tier 3 gets a tier-2
  regression reproducing it via fakeclaude.
- A tier-2 test calls `t.Parallel()` first and owns its data directory, ports
  and stand-in Telegram or Slack (#182). Two kinds run in series and say why:
  a real-docker test is a row of `TestDockerRows`, since hubs on one image
  share the egress wall (ADR-0028), and a `t.Setenv` test.
- The web's tier 1 is pure logic (formatting, timeline entries, form gates,
  API type guards): logic worth testing moves into a module of its own, a DOM
  environment is opt-in per file only where a markup defect has bitten, and
  browser automation is a manual tool, never CI.
- Quality baselines (perf, scale envelope, reliability and security rules) and
  the full CI gate list live in `docs/QUALITY.md` (ADR-0013);
  arch tests enforce the seams, and a perf smoke at the scale envelope runs
  with tier 2.
- fakeclaude scenarios are declarative fixtures in the repo, and fakeclaude
  tracks the real CLI's observed behaviour, version-noted like
  `claude/preflight.go`'s TestedVersion; `cmd/fakeclaude/FIDELITY.md` records
  each modelled behaviour as verified or assumed, under the review rule in
  `docs/QUALITY.md`.
- A scenario reaches a loop by a `.fakeclaude` file in a bare loop's
  workspace, or by the `FAKECLAUDE_SCRIPT` secret, the only route into a
  contained loop and so the one for a row that runs on both runtimes.

## Go

- Format/lint: `gofmt`, `go vet`, `golangci-lint`, all CI-gated; no style
  debates beyond those and the naming rule.
- **Names say what they hold.** No single-letter variables or receivers
  (`actor`, not `a`), except `i`/`j` as loop indices, `t *testing.T`, and
  `w http.ResponseWriter` with `r *http.Request` as parameters.
  `scripts/shortnames` checks it in `make lint` against a baseline that only
  shrinks: rename what you touch and lower the count.
- **Stdlib-first**: see **Dependencies** under Agent workflow; the rule is the
  same in both languages.
- Errors: wrap with `fmt.Errorf("…: %w", err)`; sentinel errors as package vars
  (`store.ErrNotFound` pattern); no panics outside `main` wiring.
- `context.Context` first parameter on anything that blocks or touches I/O.
- Interfaces are defined where they're consumed (the hub owns the seams), kept small.
- Tests: table-driven, `t.Run` subtests; no test frameworks.
- Time: unix milliseconds `int64` everywhere (DB, API, bus). IDs: TEXT UUIDs.

## Comments

- **Self-contained.** A comment states its reason in place. A bare `#n` or
  `ADR-nnnn` may point at the history (#368), but the comment reads correctly
  without it: never "see #n" or "per ADR-nnnn" instead of the reason, and no
  ADR section numbers or milestones, which drift. `scripts/comment-refs.sh`
  fails `make lint` on an ADR section number in a Go, TS or CSS comment; the
  rest is review-enforced.

## Web

- TypeScript strict; React function components; TanStack Query for server state; no
  component library, no CSS framework — hand-rolled `styles.css` on the design tokens.
- Prettier + ESLint, CI-gated. API types live in `src/api.ts` only, mirrored by
  hand from the Go structs (snake_case JSON) and checked in review until the
  API freezes at L6.
- A new `spool` event subtype ships with its timeline copy in `src/timeline.ts`
  in the PR that emits it: `spoolNote` renders nothing for a subtype it doesn't
  know.
- A control's dressing (well, hairline, radius, focus ring, disabled
  treatment) sits on `input`, `textarea` and `select` themselves, never on a
  wrapper class; a wrapper sets width and layout, a variant only what it
  changes.
- A control not accepting input mutes its text to `--text-muted` and drops its
  well to the canvas (ADR-0027 §11), never `opacity`, which falls below the
  contrast floor.
- An error the room draws carries `role="alert"` (#354): `.form-error`, or
  `.load-older-failed` in the timeline. `src/alerts.test.ts` checks both, and a
  new error class joins its list.
- **No em dashes in UI text** (#331, the operator's call): labels,
  placeholders, hover titles, notices and timeline lines use a colon, comma,
  full stop or parentheses; an option label separates value from description
  with a middle dot (`opus · deep agentic work`), and an empty cell shows a dim
  middle dot. `npm run lint` refuses one under `src/` (`eslint.config.js`,
  #1013), comments and tests excepted.
- Message and timeline text is untrusted and renders as text, no HTML or
  markdown. Only `<Linked>` (`src/components/Linked.tsx`, `src/linkify.ts`)
  reads it, linking only `http://` and `https://` URLs; an anchor shows the URL
  itself and opens in a new tab with `rel="noopener noreferrer"` (#759).

## API

- REST under `/api/`, method-routed stdlib mux, JSON snake_case. SSE for streams.
- Every mutating endpoint validates server-side; the UI's dropdowns are
  convenience.
- A write whose subject is gone answers 404, not 500: the store returns
  `ErrNotFound` and the handler maps it with `storeErr`; 500 means the server
  broke.
- Secrets (bot tokens, connection creds) never appear in API responses (`json:"-"`).
- A sentence the hub writes for the room wraps each literal the operator types
  or copies (a command, flag, scope or settings label) in single backticks,
  which the room renders as a copy chip, and backticks mean nothing else: no
  nesting, no escaping (#870, #871). An interpolated value goes through
  `roomtext.Plain`, as `jsonErr` does for its string arguments; a platform's or
  tool's text is plained in its error type where it comes in, and a sentence
  built elsewhere plains where it fills one in (#959).

## Prompts

- Everything injected into a loop (system prompt, envelopes, preambles) lives
  in `internal/loop/prompt.go`, never inline elsewhere; changing an envelope
  header is a `feat`.

## Dev environment

- `make` targets are the canonical entry points: if it isn't in the Makefile,
  it isn't a supported workflow. Go is pinned in `go.mod`;
  Node 20.19+ or 22.12+ (vite 7's floor); `.editorconfig` at root.
- `AGENTS.md` (with `CLAUDE.md` symlinked to it, ADR-0015) orients agent
  sessions and loops: read VISION → ARCHITECTURE → CONVENTIONS, then the
  package you're touching. It links, it doesn't duplicate.
- Real-claude e2e (tier 3) spends plan tokens: run it deliberately, prefer
  haiku, and note the run in the PR.
