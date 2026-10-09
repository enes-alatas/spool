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
  `git log --format=%s -50` as well as this list:
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
  - **Length**: about 72 characters is the working ceiling. Almost nine in ten
    subjects on `main` fit it, and one past 80 is a subject to cut.
  - **No closing keywords in a commit message**, subject or body: `close`,
    `closes`, `closed`, `fix`, `fixes`, `fixed`, `resolve`, `resolves` or
    `resolved` next to `#n`. GitHub closes the issue when the commit reaches
    `main`, whatever the PR meant, which is how #80 was closed by a commit that
    said it "closes #80's design questions". The issue link belongs in the PR
    body (below), where closing is deliberate and reviewed. A commit may say
    `Part of #n` or name `#n` in prose, since neither closes anything. Write
    around it: "settles #80's design questions".
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
- **One PR, one issue** (#462). An issue that will take more than one PR is
  split before its first PR opens: each slice is a typed sub-issue of it,
  filed under the epic-children rule above, with one owner and its own
  acceptance bar, and each PR closes exactly one slice. The parent keeps only
  the contract and the recorded decisions, and closes when its last slice
  does. Posting a slice plan on the parent is the trigger: whoever posts it
  files the slices in the same sitting. Without them a reader reconstructs
  from the PR list which slice is done, in review or next (#123, #420).
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
  (the MVP is retroactively v0.1.0). `v1.0.0` is OSS launch (L6).
- **The operator tags, never a loop** (#223). Release notes are generated
  from the commit titles; there is no CHANGELOG file. Procedure, and how a
  build names itself: `/release` (`.claude/skills/release/`).

## Secrets and evidence

*Written after 2026-09-18, when a bot token reached a log line, then an issue
body quoting that log, then a test asserting that tokens do not leak. The old
rule — no secrets in code, logs or API responses — already covered two of those
three, and they happened anyway. What was missing was not coverage but the
habits below.*

- **Secrets never land in code, logs, API responses — or evidence.** Evidence is
  everything a change carries with it: issue and PR bodies, review comments, commit
  messages, fixtures, attachments, chat. A rule about code alone leaves every
  route a person takes.
- **Quote credentials redacted.** A credential-shaped string lifted from a log,
  response or transcript is written as at most eight characters followed by `…`,
  fewer if that already identifies it. A structural prefix counts toward the eight
  (`ghp_abcd…`) unless the prefix is itself longer, in which case the eight are
  counted after it (`sk-ant-api03-Xf9k2b…`) — a reader has to be able to match the
  value against the store, and a quote that is all prefix identifies nothing. Never
  the whole value, not even in a private repo: edit history outlives the redaction.
- **Fixtures are synthetic.** A fixture needs a whole working-shaped value, so
  redaction is not available to it: never paste a real credential into a test, a
  seed script or a mock response — write one that is obviously fake
  (`0000000000:AA-not-a-real-bot-token`). This is the one place the rule above
  cannot help, which is what makes it a separate rule.
- **Screenshots come from fixtures.** A control-room screenshot in a PR is driven
  against fixture data, never the live fleet — the timeline renders raw
  assistant text and tool inputs, so anything a loop ever echoed is in the picture.
  A loop's Undelivered pane (#263, #281) lists the full body of every message
  that failed to send, an undelivered owner DM among them, and is covered by
  being *in* the standard set: a shot of it taken any other way is the
  violation. The Activity page carries the same kind of text and is
  screenshotted never, not even from the fixture. That is the operator's
  standing decision, in the fleet rules every loop runs under (#277), and not
  a gap in the fixture argument: a fixture shot of Activity would be as safe
  as the Undelivered one. The page whose whole content is the operator's own
  conversations stays out of every picture, so whether a shot of it came from
  the fixture is a question nobody ever has to answer.
  **`make ui-shots` is how** (#248): it seeds a throwaway store with an invented
  fleet (`cmd/uifixture`), starts a hub on it, and writes the standard set —
  Fleet, the fleet channel, a loop timeline, New loop, Access, Rules, Settings and its Egress section, a loop's Undelivered pane, a
  loop's Connections panel, the Connections page, the mission editor, a bare loop's
  move into a docker container —
  to the gitignored `web/shots/`. The hub and its store are gone the moment the
  command returns, so a page the set does not cover is covered by adding it to
  the `shots` list in `web/scripts/ui-shots.mjs` — there is no hub left to
  shoot a second one against. Taking one by hand against a dev server pointed
  at a real fleet is what the rule forbids, and it is how the thirteen
  live-data images the go-public audit found were made.
  **`make demo`** records the README video the same way (#447): Playwright
  drives the room on a fixture hub, and Remotion (`web/demo/`, a package of
  its own that the binary never carries) composes and renders it. The mp4 is
  uploaded as a GitHub attachment, never committed. Remotion is
  source-available, not open source: it is free for individuals, non-profits
  and companies of up to three people, and anyone else needs a company licence
  from remotion.pro before running the target.
- **A live credential in front of you is an incident.** Stop, do not copy it,
  redact it where you can still reach it, and tell the operator privately;
  the value never goes in the group.
  Procedure: `/credential-incident` (`.claude/skills/credential-incident/`).
- **Secrets at rest are private by mode.** Everything Spool writes — the database
  with its tokens and injected secrets, the loop homes with their transcripts and
  checkouts — lives under a `0700` data directory, and the files Spool owns there
  are `0600`. `internal/datadir` applies this at every startup, narrowing what it
  inherits, because the default umask does not: world-readable is how a token
  that had only reached a log became readable to every account on the box. A new
  file under the data directory that carries secrets joins that list in the PR
  that adds it.
- **A stored credential is sealed.** The store seals every credential it keeps
  under the hub key (ADR-0046). A new column that holds one joins
  `sealedColumns` in `internal/store/sqlite/seal.go`, and a new settings key
  joins `sealedSettings` beside it, in the PR that adds it, and `forgetSecrets`
  there clears it. Its table writes it sealed and reads it opened, and never
  compares it in SQL.
- **Credential inputs are masked and decline autofill.** An input that takes a
  credential is `type="password"` with `autoComplete="off"`, wherever the value is
  entered — a first entry is as exposed as a replacement, and the field a form
  forgets to type is the one left standing in cleartext. `off` is best-effort:
  browsers honour it unevenly on password fields. The stronger-looking
  `autoComplete="new-password"` is deliberately not used — it invites the browser's
  password generator to offer to invent a value for a field that must hold a token
  the user was given, which is worse than the autofill it prevents. Name fields
  beside a credential (a secret's NAME) are not credentials and stay unmasked.
- **Loops are bound by this too**, through a fleet rule the operator sets; this
  section is what that rule points at.

## Agent workflow (ADR-0015)

*The same loop a human follows, written down because most contributors are
agents — Claude sessions today, Spool's own loops from L2.*

- **Lifecycle**: typed issue → branch `<type>/<topic>` off fresh `main` →
  implement → self-check → PR (template filled, issue linked) → stop for
  review. **Fresh means synced**: `git fetch` and branch off `origin/main`,
  never a local `main` you haven't updated — in an agent session the checkout
  can lag the remote (a dependency PR may already be merged under new SHAs), and
  branching on a stale base costs a needless rebase or, worse, duplicate-looking
  commits. Reviews may come from humans or agents; merging needs a human
  approval (ADR-0008), and agents never merge their own work.
- **Code is not precedent** — the docs of record are; where code and docs
  disagree, docs win. Improve opportunistically or rewrite behind the seams.
  Neither is a file's local habit precedent: a pattern the neighbouring lines
  have settled into may be one contributor's from last week, and citing it back
  is how a rule already on the books gets reviewed past (#141). Check the doc,
  not the neighbours.
- **Issue-first**: any change touching behavior, seams, dependencies, prompts,
  or the docs of record (VISION, ARCHITECTURE, CONVENTIONS, QUALITY, ADRs)
  starts from a typed issue the human could have seen. Trivial mechanical fixes
  (typo, formatting, broken link) may go straight to PR — the template's Issue
  section says why.
- **Secret shapes are scanned, not remembered**: `make secret-scan` runs the same
  check CI runs on every PR — the lines a change *adds*, against the known
  credential shapes. Test files and fixtures may hold a token-shaped string only
  when the value reads as obviously synthetic; production code never may. A hit
  prints at most eight characters, because a CI log is an artefact too.
- **An issue or comment body is redacted, not rejected** (#206): the
  `issue-guards` workflow edits the value out, but the revision history still
  holds it, so rotation is not optional, and a loop reports it privately for
  the operator to delete. Procedure: `/credential-incident`.
- **The board is world-readable** (ADR-0031): once the repo is public, every
  issue, comment, review and commit is readable by anyone, history included,
  and a deletion is not a recall. One rule follows, which the redaction rules
  enforce only the edges of — a bot handle and the group's chat id are taken
  out of a body automatically, the words are not: **nothing said in the
  fleet channel, or the Telegram group it is mirrored to, is quoted on
  GitHub.** The channel is where the fleet coordinates and the operator
  thinks out loud, and neither was written for an audience; an issue or PR that needs the substance
  of something said there states it in its own words, as the writer's own
  claim.
- **Proving an event-driven workflow** (#209): a workflow firing on anything
  but `pull_request` or `push` declares `workflow_dispatch`, and a PR that
  adds or changes one links a green dispatched run from its own branch in
  Verification. A brand-new workflow, which cannot be dispatched yet, is the
  exception until it lands, when its author dispatches it and reports the run
  on the PR. Procedure: `/prove-event-workflow`
  (`.claude/skills/prove-event-workflow/`).
- **Definition of done** — self-check before opening a PR:
  `make lint && make test && make itest` green locally, plus `npm run lint`,
  `npm run format:check` and `npm test` in `web/` when the change touches it;
  engine behavior carries a tier-2 test; docs of record updated in the same PR
  when behavior or rules changed; an ADR when a constraining decision was made.
- **Ask vs proceed**: implementation details — reversible choices inside
  existing rules — proceed autonomously. Anything that constrains the future
  stops for a human interview and gets an ADR: a new dependency (a dev-only one
  skips the ADR, see Dependencies), a seam or prompt-contract change,
  terminology, public API shape, security posture, or spending real plan tokens
  (tier-3 e2e). A question from the operator is a question. Reprioritizing or
  interrupting in-flight work takes an explicit yes; finished work gets its PR
  before any redirect. **A decision is not a reorder** either (#462): backlog
  talk with the operator reaches a developer only once it is a decision, and
  the decision joins the back of that developer's queue. Whoever relays it
  files the issue, notes the queue position on it, and holds the assignment
  until the developer's current PR merges. Only an explicit "first" or "now"
  from the operator moves it ahead, and even then not past work one step from
  its PR.
- **Dependencies** are stdlib-first in Go and the control room alike (#266).
  Every new one stops for a human interview, whatever it reaches, and is
  argued in its PR description. One the shipped binary carries (a module the
  binary's non-test code imports, or an entry in `web/package.json`
  `dependencies`) reaches every operator's install, so it also gets an ADR the
  PR cites, as `modelcontextprotocol/go-sdk` took ADR-0026. A `devDependency`,
  or a tool that runs only on a maintainer's machine or in CI (a Go module
  only tests or a `tool` directive use), can be removed without changing what
  an operator runs, so it takes no ADR, unless it adds or changes a CI gate,
  which amends ADR-0013 as Vitest did. The manifests are where to look, not a
  copy here: `go.mod`'s `require` block holds both kinds, so check what
  imports a module; `web/package.json` splits them itself.
- **Flag and continue** is the third state, between those two (#192). Where
  stopping would idle a PR, the author may put the question to the operator and
  keep working — but only when the work stays correct under either answer, or
  the answer is cheap to fold. The PR carries the `Operator decision pending:`
  line until the answer arrives, and the answer is recorded where the decision
  lives (the issue's body, or an ADR), never only in chat.
- **Refused at the tool call** (ADR-0042): a loop's Bash calls pass
  `spool-hook` first, and the mechanical rules it knows are refused before the
  command runs, with the reason in the loop's turn. Today that is the host's
  shared state: a `git stash` with no named entry (push without `-m`, `pop`,
  `apply` or `drop` with none named, `clear`), `pkill` and `killall` (kill by
  PID), a force push to or deletion of `main`, and `git add -A`/`--all`/`.`
  (stage by path). The hook catches slips. A rule it enforces still binds a
  human, and a rule it does not know binds everyone as before.
- **Scoped delegation**: the interview may end with the human delegating the
  decision back ("you decide"). That delegation is per-decision: it covers
  exactly the questions put to the human, and the ADR or issue recording the
  outcome quotes it verbatim in its Context. A standing delegation — a class
  of decisions the fleet may settle without asking — is itself a constraining
  decision and needs its own ADR.

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
  `actor`, not `a`; `stream`, not `s`. Tooling can't catch this and a reader
  three months later can't guess it, so it's a review rule. Exceptions, because
  they're universal in Go and carry no domain meaning: `err`, `ok`, `ctx`, `i`/`j`
  as numeric loop indices, `t *testing.T`, and `w http.ResponseWriter` with
  `r *http.Request` as a handler's parameters. The MVP code predates the rule —
  don't add more, and rename what you touch.
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
  dot (`opus · deep agentic work`), and an empty cell shows a dim middle dot. Code
  comments are not UI text.

## API

- REST under `/api/`, method-routed stdlib mux, JSON snake_case. SSE for streams.
- Every mutating endpoint validates server-side (the UI's dropdowns are convenience,
  not enforcement).
- A write whose subject is gone answers 404, not 500: the store returns `ErrNotFound`
  and the handler maps it with `storeErr`. 500 means the server broke.
- Secrets (bot tokens, connection creds) never appear in API responses (`json:"-"`).

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
