# ADR-0044: The hub checks the Claude login with one haiku answer, on save and on request

Date: 2026-10-04 · Status: accepted (operator decision of 2026-10-04 15:34 UTC, recorded on #580; shape on #585) · Amends: ADR-0033 item 6 (a CLI run outside a loop's turns that uses the operator's login)

## Context

The first-run page's harness pillar (#580) has to say whether claude can
log in. Until now the hub could only infer it. A refused turn puts a loop down
`unauthenticated` (#405), and a finished turn proves the login worked. Before
any loop wakes, it knows only that a setup-token is saved, and a wrong one
reads as fine until the first wake refuses it.

Nothing cheaper answers the question. Measured on 2026-10-04 with CLI 2.1.288
and no tokens spent (#580):

- `claude auth status` reads local state only. With a bogus token in
  `CLAUDE_CODE_OAUTH_TOKEN` it still answers `loggedIn: true`.
- ADR-0033's resolution run reaches no API by design, so it proves nothing
  about the login.

The only check that tells a good login from a bad one asks the API. ADR-0033
item 6 says such a run "is not one of these and needs its own ADR". This is
that ADR.

## Decision

1. **A login check is one print-mode turn on haiku with a one-word prompt.**
   The hub reads the run to its result:
   - the CLI's own `authentication_failed` message (`IsLoginRejected`, #405)
     is a refusal, and its sentence is kept;
   - a result without an error is a login the API accepted;
   - any other end proves nothing either way.

   The run can do nothing with the answer:
   - `--tools ""` gives it no tools;
   - `--strict-mcp-config` with no config gives it no MCP server;
   - `--no-session-persistence` leaves no session behind;
   - no permission mode is passed, so a tool the model asked for anyway
     would be refused, not run.

2. **It runs the login a new loop gets, the way that loop would run it.**
   - **Docker hub:** a throwaway container of the default workstation image,
     behind the same egress wall as a workstation (ADR-0028). The setup-token
     crosses as a value-less `--env` resolved from the client's environment,
     as a wake's variables do, so it is never in argv. HOME and the config
     directory are fresh under `/tmp` and go with the container, which is
     labelled and swept like a resolution container.
   - **Bare hub:** the host's `claude` with the hub's own environment, as a
     bare loop runs, in a temporary directory removed afterwards.

   Each check is bounded at 90 seconds.

3. **It runs only on the operator's action:**
   - when a setup-token is saved on a docker hub;
   - when the operator asks, through `POST /api/onboarding/harness-check`.

   Never on a timer, at start, or per read. Each check spends one short haiku
   answer from the operator's plan. Removing the token forgets the last check.
   A bare hub's loops don't run on the token, so saving one there checks
   nothing.

4. **The hub keeps the last check's outcome, not its output.**
   - The stored record is a status (pending, ok, refused, inconclusive), the
     refusal sentence, and when the check began. It lives in settings, so it
     survives a restart. A check a stopped hub left pending reads
     inconclusive once the 90-second bound has passed: nothing else would
     ever end it.
   - A newer check supersedes one still running, and only the newest outcome
     is kept.
   - Why an inconclusive check ended goes to the log only: a run's stderr is
     no place to read back to a browser.

5. **The harness pillar weighs the check against the turns.**
   - A loop down `unauthenticated` says no, whatever else says yes.
   - On a docker hub with no token, the pillar is not done.
   - Otherwise the newer evidence decides: the last check, or the last turn a
     loop finished without an error. Saving a token starts a check, so a turn
     from before the save says nothing about the new token.
   - The reason names the evidence ("the login check authenticated", "a turn
     authenticated") or what is missing ("setup-token saved; not checked yet",
     the refusal sentence).
   - While a check runs, the pillar carries `checking`, so the control room
     can wait on it without keying on the reason's wording.

6. **ADR-0033 item 6 now lists four ways the CLI is invoked:**
   - a loop's turns;
   - `claude --version`;
   - alias resolution;
   - the login check.

   The first three keep their rules. The login check is the one run outside a
   loop's turns that holds the operator's credential and reaches the API, and
   items 1–3 here are its rules. A further run of that kind needs its own ADR
   in turn.

## Consequences

- A wrong or revoked token shows on the first-run page within seconds of
  being saved, before any loop depends on it.
- Every save and every requested check costs one haiku answer. The real check
  for #585's evidence took 3.6 s and was accepted.
- The tier-2 suite drives the check through fakeclaude:
  - a check read while it runs, held by a new `login-slow` state;
  - an accepted check;
  - a refused one, through the existing `login-expired` state;
  - a turn after a refused check, which wins as newer evidence;
  - the docker check, run in the workstation image.

  fakeclaude ignores the new flags; `login-slow` is its one addition.
- The check rests on CLI 2.1.288's `--tools ""` and `--no-session-persistence`
  flags. A CLI that dropped either would fail the run, which the check treats
  as inconclusive, never as a login refused.
