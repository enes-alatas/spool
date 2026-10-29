# Spool — Conventions

*Living document. These rules bind humans, Claude sessions, and (from L2) Spool's own
loops equally. Keep it short; if a rule needs a paragraph of justification, it gets an
ADR instead.*

## Workflow (humans in the loop)

- **PR-only, no exceptions** (ADR-0008). `main` is protected; every change — human,
  Claude session, or loop — lands via a pull request with CI green and at least one
  human approval. Loops never merge their own work.
- **Rebase & merge only** (ADR-0016): the branch's commits land in `main`
  verbatim — linear history, no merge or squash commits (repo settings enforce
  this). The PR title is still a valid Conventional Commit; the template's
  Commit Structure section lists what will land.
- **The merge queue** (ADR-0054, #819): an approved PR gets the `queue` label
  from its reviewer, and the merge ask asks the operator to turn on
  auto-merge. The queue tests it rebased onto the `main` it lands on, and its
  `queue` status says where it stands, so nobody rebases a PR only because
  `main` moved. A PR that conflicts or fails there is red, and its author
  rebases or fixes it. Any push to a queued PR takes it out (label and
  auto-merge off), so a new head goes back through review and the merge ask.
- **Atomic commits** (ADR-0016): one logical change per commit — a subject that
  needs "and" means split. Each commit builds and passes tiers 1–2 on its own
  (history stays bisectable; review-enforced — CI tests the head). Mechanical
  churn (gofmt, renames, moves) never mixes with behavior changes. Every commit
  is a valid Conventional Commit — if no single type fits the diff, split it.
- **Review feedback folds into the origin commit** (ADR-0016; procedure:
  `/review-fix`): no "address review" commits — rewrite the branch, prove the
  fold with a byte-identical tree diff, push `--force-with-lease`. Genuinely
  new scope gets its own commit, or its own issue and PR. Accepted costs:
  inline comments go outdated; the changes-since-last-review diff resets.
- **A review's last word is one of two things, never both** (#141): either
  "approved at `<sha>`; merge as-is — remaining notes are follow-ups", or
  "notes to fold; not ready for merge". An approval that also leaves fixes
  hanging is how a PR gets merged out from under its reviewer (#165). A new
  push voids the approval: the author re-pings the reviewer, never the
  operator.
- **The merge request comes from the reviewer**, in the same message as the
  final approval, with the PR link. Authors never ask for a merge — if the
  reviewer marked fixes optional, the author either declines them on the PR or
  folds them and waits for re-approval. Either way the reviewer pings last
  (#170).
- **A PR labelled `needs-security-review` waits for the security review** (#742).
  `pr-guards` adds the label when a PR changes a path in
  `.github/security-review-paths`; anyone adds it by hand to a PR that makes
  the hub trust something a loop controls without touching the list (an
  answer read from inside a workstation, say). The security reviewer (the
  DevSecOps role) reviews it as a peer and says so in a PR comment, which may
  be "nothing from me". The reviewer who sends merge requests sends none on a
  labelled PR until that comment exists at the current head, or at a head
  whose later pushes are a bare rebase. A fork PR is not labelled
  mechanically (its token is read-only), so its reviewer labels it by hand.
  On a PR the security reviewer authored, its Notes / Risks section stands as
  that comment: it says what the change opens and why that holds, and the
  reviewer who sends the merge request checks it is there.
- **A gh body names people without an @ and ends with a signature** (#630).
  The loops share one GitHub identity, and the loops' names and the people's
  chat handles belong to strangers there, so an `@` before one pings whoever
  owns that account. A signature line, `— Name · Role`, is how a reader
  tells the loops apart. This repository's own PreToolUse hook
  (`.claude/settings.json`, `.claude/hooks/gh-body-guard.sh`) refuses a
  `gh issue|pr create|comment|edit|review|close|reopen` or `gh api` call
  whose body breaks either rule, or whose body it can't read before the
  call runs (a variable or `$(…)`); the names are in
  `.claude/hooks/fleet-names`. It lives here and not in the hub's hook
  (`internal/hook`) because these are this team's conventions, not Spool's.
  Claude Code reads `.claude/settings.json` only for a session started in a
  checkout, and a loop's session starts in its home, so each loop adds the
  hook to its own `~/.claude/settings.json`, run from its clone's
  `origin/main` (here `~/spool`):
  `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "sh -c \"$(git -C \"$HOME/spool\" show origin/main:.claude/hooks/gh-body-guard.sh)\" gh-body-guard \"$HOME/spool\""}]}]}}`.
  Release notes, a merge commit's body and `gh api graphql` are not
  checked. A body that gets past the hook anyway, from a session without
  it or from the web, is flagged after the fact: `issue-guards` and
  `pr-guards` add `fleet-mention` to the issue or PR when an issue, PR or
  comment body has an @ before a name in that list, outside code. The ping
  has gone out by then; the author takes the @ out, and whoever sees the
  label fixed removes it.
- **The board sweep reads the pinned "CI health" issue** (#216, mechanism #210).
  A workflow that fires on issues, comments or a schedule reports its failures
  nowhere a PR can show them, so `ci-health` posts each one there as a comment.
  A new comment is triaged on the sweep it appears in: open a `bug` linking the
  run, and leave the comment as the record that the failure was seen. Silence
  there is the normal state and is not evidence the sentinel is alive — that is
  proven by dispatching it at a known-failed run, which a PR touching it links.
- **A pending operator decision blocks the merge** the way red CI does (#192).
  A PR resting on a question still with the operator carries, in Notes / Risks,
  one live line per question, citing where it was asked so the reviewer can
  check it is real and still open:
  `Operator decision pending: <the question> (<#n or link>)`. Live means
  outside an HTML comment — the template reproduces the marker in its own
  comment, and a commented marker blocks nothing. The reviewer may review and
  approve while one stands — that is an approval *pending*, not a mandate to
  merge — but issues no merge request until the line is gone. The author
  deletes it once the answer lands and reports that like ready-for-review (a PR
  comment naming the answer and where it is recorded, plus a ping to the
  reviewer): such a question usually resolves with no push, so nothing else
  would wake them. An answer that changes the work voids the approval exactly
  as a push does (#188).
- **Branches**: `<type>/<topic>` (e.g. `feat/sandbox-runtime`, `fix/trailer-clamp`).
  The `loop/<name>` namespace is reserved for loop worktree branches — never use it
  for feature work.
- **Commits / PR titles**: [Conventional Commits](https://www.conventionalcommits.org)
  — `feat|fix|refactor|docs|test|chore|ci(scope): summary`. Scopes are package names
  (`loop`, `route`, `surface/telegram`, `web`, …). The subject rules below
  (#76) are checked by the author before the PR opens, against
  `git log --format=%s -50` as well as this list. `pr-guards` checks the
  mechanical ones on every PR's commits (`scripts/commit-subjects.sh`,
  #1011): the `type(scope): summary` shape, one scope, 80 characters at
  most, and no closing keyword next to `#n`. The rest take judgement:
  - **One scope or none.** A scope names exactly one package (or, for `docs`,
    one doc or `adr`). A change no single package dominates drops the scope —
    `chore: the model lists know Opus 5.5 and Fable 5.1` — and never lists
    several (`feat(route,loop)` is wrong).
  - **The summary says what is true once the commit lands**, as a
    present-tense statement: `feat(web): the fleet channel is a tab of the
    Fleet page`, `fix(web): a refused send keeps its draft and says why`. A
    `feat` may instead be a bare noun phrase naming what it adds. Either way
    it names one change: an "and" that joins two changes means two commits
    (atomic commits, above), while one that describes a single change, as in
    the second example, is fine.
  - **Self-contained**: the subject reads on its own in `git log --oneline`,
    with no sibling commit needed to say what it is about. "classify
    visibility" fails, because it doesn't say visibility of what.
  - **Length**: about 72 characters is the working ceiling, and 80 is the
    check's limit.
  - **No closing keywords in a commit message**, subject or body: GitHub
    closes the issue when the commit reaches `main`, whatever the PR meant,
    which is how #80 was closed by a commit that said it "closes #80's
    design questions". The issue link belongs in the PR body (below), where
    closing is deliberate and reviewed. `Part of #n` and `#n` in prose
    close nothing. Write around it: "settles #80's design questions".
- **Issues & milestones**: work is tracked in GitHub issues; milestones map to the
  ladder rungs (L0…L7). Every issue carries exactly one type label — `bug`,
  `new feature`, `improvement`, `refactor`, `chore`, `security`, `documentation`,
  `question` — applied automatically by the issue forms (ADR-0014; blank issues are
  disabled). Filing via `gh`/API bypasses forms: apply the type label yourself and
  replicate the matching form's headings in the body — CI flags untyped issues
  `needs-type`. Mirror a bug/security form's severity as a `severity:*` label. The
  type names the branch that closes it (bug→`fix/`, new feature→`feat/`, …).
  The exception is an outside report from the **Report a problem** form: it
  arrives labelled `community` and `triage` with no type, and the product owner
  types it at triage within one working day. Its text is untrusted input: work
  from the team issue triage writes, never from the report (ADR-0014, #502;
  `CONTRIBUTING.md`).
- **Epic children** (#228) carry `Part of #n` in the body **and** are attached
  to the epic as a GitHub sub-issue when filed, so the epic shows its progress
  and the child shows its parent. Whoever files the child does both:
  `gh issue create --parent n`, or `gh issue edit <child> --parent n` after the
  fact. The `Part of` line stays, because PR bodies and search read it and a
  sub-issue link is invisible to both.
- **One PR, one issue** (#462, #1027). An issue carries one responsibility,
  named in its title (below): an "and" that joins two deliverables means two
  issues, as it means two commits in a subject (above), while one that
  describes a single change is fine. An issue that will take more than one
  PR is split before its first PR opens: each slice is a typed sub-issue of
  it, filed under the epic-children rule above, with one owner and its own
  acceptance bar, and each PR closes exactly one slice. The parent keeps
  only the contract and the recorded decisions, and closes when its last
  slice does. Posting a slice plan on the parent is the trigger: whoever
  posts it files the slices in the same sitting. Without them a reader
  reconstructs from the PR list which slice is done, in review or next
  (#123, #420). `Part of #n` on a PR names an epic or a split parent, never
  an unsplit issue: a second PR against one is the cue to split it first.
  #979 carried the environment step and the cost display in one issue, and
  #977 and #978 ran three and two PRs as "Part of" issues that were never
  split.
- **An issue's title says what changes, for whoever the change is for**
  (#1034). A product issue's title is one sentence in the operator's words
  saying what changes for them, in the room, on a surface or in a deploy,
  with no ADR, slice, PR or symbol name in it; those go in the body, under
  "Seams / ADRs touched" where the form has it. A team-only issue (CI,
  tests, docs of record, repo guards) is written for the team: its title may
  name the test or workflow it is about, since that is what changes, and may
  open with a Conventional prefix, `type(scope):` or `type:`, matching the
  commit that will close it. A product issue carries no prefix, and neither
  kind puts "Part of #n" or "web half of" in its title. A product issue's
  body opens with what the operator sees or does today and what is wrong
  with it; references and internals follow. Its proposed shape names each
  surface that changes, so a screenshot comment can say which one it shows;
  a shot of something the issue doesn't name means the issue is missing a
  line, or the shot belongs to another issue. #1023's title named a slice, a
  PR and a symbol before what changed, and the operator could not tell what
  it was about or how its screenshots related to it.
- **The issue body is the spec of record; comments are history** (#388). A
  loop rotates and reads the body first, so a decision that exists only in
  a comment gets asked again: #230's body still called the WebSocket
  dependency open four days after the operator settled it. Whoever records
  an operator decision on an issue amends the body in the same sitting. The
  affected section states the decision with its date, and the Open
  questions entry is removed or marked settled. The body may cite the
  comment, but a reader of the body alone must get the current answer. A
  reviewer checks the body against the thread before approving a PR that
  closes the issue.
- **PRs** follow `.github/pull_request_template.md`: link the issue (`Closes #n` /
  `Part of #n` — if none, say why), and cite ADRs for seam changes and new deps.
  The exception is Dependabot's weekly `ci:` PR bumping action pins (#707).
  It carries no issue link, template or signature. Its reviewer checks
  three things: each new SHA is the commit of the release tag in the pin's
  comment and the PR's release notes,
  nothing in those notes widens what the action does with the job's token,
  and CI is green. Then the reviewer sends the merge request as for any PR.
  A bump the reviewer won't take is closed with the reason, never left
  open.
- **A feat names its other half** (#634). A `feat` PR that changes
  `internal/` but not `web/`, or `web/` but not `internal/`, carries an
  `Other half:` line under the template's Issue section: the sibling issue
  (`#n`), `none — <reason>`, `API-only` or `UI-only`. The `pr-guards`
  workflow fails the PR without one, or when `#n` names no issue (a pull
  request's number included). v0.4.2 was tagged with #631's hub route and no
  room to use it, because the pairing lived in one reviewer's head. The
  check proves only that the line is there: the reviewer approves a one-half
  feat only when its other half is filed, or argued away on the PR.
- **Decisions**: any choice that constrains future work gets an ADR in `docs/adr/`
  (sequential number, `proposed → accepted → superseded by ADR-XXXX`). Living docs
  (VISION / ARCHITECTURE / CONVENTIONS) state current truth and link the ADRs.
- **Amending an accepted ADR** (#141): a change that narrows, extends or
  clarifies a decision is a dated `**Amendment (YYYY-MM-DD, #n):**` block
  that adds lines and never touches an existing one, with the date added to
  the file's `Amended:` header; a reversal is a new ADR. The three exceptions, supersession of one clause, and the reviewer
  bar: `/amend-adr` (`.claude/skills/amend-adr/`).
- **Releases**: semver, `v0.<rung>.x` — completing ladder rung Ln tags `v0.n+1.0`
  (the MVP is retroactively v0.1.0). `v1.0.0` is OSS launch (L6). The
  README names what the release ships: a docs issue lists the release's
  additions, says whether the demo video still shows what ships
  (re-recorded if not), and lands before the tag ask (#684). The tag ask links a
  green dispatched `release` dry run on the commit to be tagged, whose smoke
  job installed its artifacts on a fresh runner (#770). A change that
  alters stored data at its first start (a re-seal, or a rewrite the
  previous release cannot read) adds its row to OPERATIONS.md's
  release-specific steps in the same PR, because generated notes cannot say
  what an operator must do about it (#712).
- **The operator tags, never a loop** (#223). Release notes are generated
  from the commit titles; there is no CHANGELOG file. Procedure, and how a
  build names itself: `/release` (`.claude/skills/release/`).
  The tag fires `release.yml`, which publishes the images by digest and
  attaches the binaries and `SHA256SUMS`, all attested (ADR-0052, #756,
  #763). A tag whose commit is not on `main` is refused. After publishing, it
  installs the release on a fresh runner the README's way; red there means
  the release is out and broken (`/release` says what then).

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
- A tier-2 test calls `t.Parallel()` first (#182). Each test owns its data
  directory, its ports and its stand-in Telegram or Slack, so tests are
  isolated by construction. Two kinds stay out of the parallel set, and
  each says why. A test that drives the real docker daemon is a row of
  `TestDockerRows`, run in series with the other rows, because hubs on one
  image share the egress wall (ADR-0028). A test that changes the
  environment every hub inherits (`t.Setenv`) runs serially.
- The web's tier 1 is pure logic: formatting, timeline entry building, form
  gates, API type guards. Logic worth testing gets lifted out of the component
  into a module of its own rather than tested through a rendered tree. A DOM
  environment is opt-in per test file and earns its way in only where a defect
  has already bitten in the markup; browser automation stays a manual
  verification tool (the screenshot practice), never CI.
- Quality baselines (perf numbers, scale envelope, reliability/security rules) and
  the full CI gate list live in `docs/QUALITY.md` (ADR-0013). Arch tests enforce the
  seams mechanically; a perf smoke at the scale envelope runs with tier 2.
- fakeclaude scenarios are declarative fixtures checked into the repo; fakeclaude
  itself must track the real CLI's observed behavior (version-noted, like
  `claude/preflight.go`'s TestedVersion).
  `cmd/fakeclaude/FIDELITY.md` is that record: one line per modelled
  behaviour, verified or assumed, with the review rule in `docs/QUALITY.md`.
- A scenario reaches a loop one of two ways: a `.fakeclaude` file in a bare
  loop's workspace, or the `FAKECLAUDE_SCRIPT` secret, which the engine injects
  into every exec. The secret is the only route into a contained loop, whose
  workspace lives inside its workstation — reach for it whenever a tier-2 row
  must run on both runtimes.

## Go

- Format/lint: `gofmt`, `go vet`, `golangci-lint` — all CI-gated. No custom style
  debates beyond that and the naming rule below.
- **Names say what they hold.** No single-letter variables, receivers included:
  `actor`, not `a`; `stream`, not `s`. Exceptions: `i`/`j` as loop indices,
  `t *testing.T`, and `w http.ResponseWriter` with `r *http.Request` as
  parameters. `make lint` and CI run `scripts/shortnames`; code that predates
  the rule is fenced by its baseline, which only shrinks: rename what you
  touch and lower the count.
- **Stdlib-first**: see **Dependencies** under Agent workflow; the rule is the
  same in both languages.
- Errors: wrap with `fmt.Errorf("…: %w", err)`; sentinel errors as package vars
  (`store.ErrNotFound` pattern); no panics outside `main` wiring.
- `context.Context` first parameter on anything that blocks or touches I/O.
- Interfaces are defined where they're consumed (the hub owns the seams), kept small.
- Tests: table-driven, `t.Run` subtests; no test frameworks.
- Time: unix milliseconds `int64` everywhere (DB, API, bus). IDs: TEXT UUIDs.

## Comments

- **Self-contained.** A comment must be meaningful on its own, without sending the
  reader elsewhere to understand it. State the reason in place. A bare `#n` or
  `ADR-nnnn` may appear anywhere in it as a pointer to the history behind the
  comment (#368), but the comment must still read correctly if that issue, PR
  or ADR is gone, since a reader should not need GitHub to understand the code:
  never "see #n" or "per ADR-nnnn" in place of the reason. Don't cite ADR
  section numbers or milestones, which drift as ADRs are amended and milestones
  close. `scripts/comment-refs.sh` fails `make lint` and CI on an ADR section
  number in a Go, TS or CSS comment; the rest is review-enforced.

## Web

- TypeScript strict; React function components; TanStack Query for server state; no
  component library, no CSS framework — hand-rolled `styles.css` on the design tokens.
- Prettier + ESLint, CI-gated. API types live in `src/api.ts` only, mirrored by hand
  from the Go structs (snake_case JSON) — until the API freezes at L6, the mirror is
  maintained manually and checked in code review.
- A new `spool` event subtype ships with its timeline copy in `src/timeline.ts`, in the
  same PR that emits it. `spoolNote` renders nothing for a subtype it does not know, so
  an unlisted event is not terse in the room — it is absent, and nobody notices.
- A control's dressing — well, hairline, radius, focus ring, and the disabled
  treatment — sits on `input`, `textarea` and `select` themselves, never on a wrapper
  class. A wrapper sets width and layout; a variant sets only what it changes. Markup
  that forgets the wrapper then renders a field, not a browser default white box in a
  near-black room.
- A control that is not accepting input says so by muting its text to `--text-muted`,
  and drops its well back to the canvas behind that (ADR-0027 §11). Never the button's
  `opacity` — on a control that manufactures a text colour below the contrast floor.
  Disabling a field while a request is in flight therefore costs no CSS.
- An error the room draws carries `role="alert"`, so a screen reader announces it
  when it appears rather than leaving it silent somewhere else on the page (#354).
  Errors are drawn with `.form-error`, or `.load-older-failed` for the timeline's
  inline note. `src/alerts.test.ts` fails on either class without the role, and a
  new error class joins its list.
- **No em dashes in UI text** (operator's call, #331): labels, placeholders, hover
  titles, notices and timeline lines use a colon, comma, full stop or parentheses
  instead. An option label separates its value from the description with a middle
  dot (`opus · deep agentic work`), and an empty cell shows a dim middle dot.
  `npm run lint` refuses one in a string, template or JSX text under `src/`
  (`eslint.config.js`, #1013); comments and test files pass.
- Message and timeline text is untrusted (a loop's words, or a stranger's) and is
  rendered as text: no HTML, no markdown. `<Linked>` (`src/components/Linked.tsx`,
  splitting with `src/linkify.ts`) is the only thing that reads it, and it makes only
  `http://` and `https://` URLs into anchors.
  An anchor shows the URL itself, never a label, and opens in a new tab with
  `rel="noopener noreferrer"` (#759).

## API

- REST under `/api/`, method-routed stdlib mux, JSON snake_case. SSE for streams.
- Every mutating endpoint validates server-side (the UI's dropdowns are convenience,
  not enforcement).
- A write whose subject is gone answers 404, not 500: the store returns `ErrNotFound`
  and the handler maps it with `storeErr`. 500 means the server broke.
- Secrets (bot tokens, connection creds) never appear in API responses (`json:"-"`).
- A sentence the hub writes for the room (an error, a status reason) wraps each
  literal the operator types or copies, such as a command, a flag, a scope or a
  settings label, in single backticks, and the room renders it as a copy chip
  (#870, #871). A backtick means nothing else there: no nesting, no escaping.
  A value the hub interpolates into such a sentence (a name, a host, a
  platform's error) goes through `roomtext.Plain`, which takes its backticks
  out, so only the hub's own words make chips. `jsonErr` does this for its
  string arguments. A platform's or a tool's text is plained where it comes
  in, in its error type, so an error passes whole and keeps the hub's own
  remedy marks; a sentence built elsewhere plains where it fills one in (#959).

## Prompts

- Everything injected into a loop (system prompt, envelopes, preambles) lives in
  `internal/loop/prompt.go` — never inline strings elsewhere. Envelope headers are
  part of the product's contract with loops; changing them is a `feat`, not a tweak.

## Dev environment

- Canonical entry points are `make` targets; if it isn't in the Makefile, it isn't a
  supported workflow. Go toolchain pinned in `go.mod`; Node 20.19+ or 22.12+ (vite 7's
  floor); `.editorconfig` at root.
- `AGENTS.md` at repo root (with `CLAUDE.md` symlinked to it, ADR-0015) orients
  agent sessions and loops: read VISION → ARCHITECTURE → CONVENTIONS, then the
  package you're touching. It links, it doesn't duplicate.
- Real-claude e2e (tier 3) spends plan tokens — run deliberately, prefer haiku, note
  the run in the PR.
