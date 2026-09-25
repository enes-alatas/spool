# ADR-0033: The hub resolves model aliases with a CLI run that can reach nothing

Date: 2026-09-25 · Status: accepted (operator decisions of 2026-09-24 on #332 and 2026-09-25 on this shape) · Amends: ADR-0001 (the CLI is run for one thing besides a loop's turns)

## Context

Since #329 the model dropdowns offer the four family aliases (`opus`,
`sonnet`, `haiku`, `fable`) so that they cannot go stale. The operator
decided on 2026-09-24 (#332) that a bare alias is not enough: every alias
shows the model id it runs as on this hub, from the start, rather than only
once some loop has run a turn on it.

The CLI is the only thing that knows. It resolves an alias locally, with no
lookup, and the id appears only inside a run. #289 ruled out the Models API
(it would mean reading the CLI's credential outside the CLI) and a real turn
on a known model (a paid inference). What is left is a run of the CLI that
cannot reach the API. That is a new kind of invocation: until now the hub ran
the CLI only as a loop's turns and as `claude --version` at preflight.

What was measured, on 2026-09-24/25 (#332, #334), with nothing reaching
Anthropic in any run:

- **The CLI resolves aliases locally and reports the id at init.** With
  `CLAUDE_CONFIG_DIR` set to an empty directory, a synthetic
  `ANTHROPIC_API_KEY` and `ANTHROPIC_BASE_URL` on loopback, host CLI 2.1.282
  reported `opus` → `claude-opus-5-5`, `sonnet` → `claude-sonnet-5`,
  `haiku` → `claude-haiku-4-5-20251001` and `fable` → `claude-fable-5-1` in its
  `system/init` event. A loopback stub that logged request paths saw the same
  ids in the `/v1/messages` body.
- **Init arrives before any answer is needed.** With the base URL on a
  loopback port nothing listened on, init arrived about 1.2 s after start. The
  CLI then retried the connection until it was killed.
- **The same holds in the workstation image with no network at all.**
  `docker run --rm --network none spool-workstation` (CLI 2.1.281) reported
  `claude-opus-5-5` and `claude-fable-5-1` at init.
- **The unauthenticated resolution matches the logged-in fleet's.** Live turn
  rows record `claude-opus-5-5` for loops on `opus` and `claude-fable-5-1` for
  loops on `fable`.

## Decision

1. **The hub resolves an alias by starting a CLI run and reading its init.**
   The run is `claude -p --model <alias> --output-format stream-json --verbose`
   with a one-word prompt. The hub reads `system/init`, takes its `model`, and
   kills the process. It never reads further, and no turn is recorded.

2. **The run gets no answer, and nothing it sends can reach anyone.** That is
   the guarantee, and it is structural: the request can only go into a
   container with no network or to a loopback port nothing reads. Even a CLI
   that found a real token could send it nowhere. On top of that, the run is
   given no credential:
   - The run's environment is built from nothing, never inherited from the
     hub: `PATH`, a temporary `HOME`, `CLAUDE_CONFIG_DIR` set to a fresh empty
     directory, a synthetic `ANTHROPIC_API_KEY` and `ANTHROPIC_BASE_URL` on
     loopback. The synthetic key only gets the CLI past its login check. It
     authenticates nothing, so this is not one of the API keys ADR-0001 rules
     out. `CLAUDE_CODE_OAUTH_TOKEN` and the per-loop secrets (ADR-0017 item 8)
     are never set. On Linux, where the login is a file under the config
     directory, this keeps the operator's login out of reach: measured. On
     macOS it is unmeasured, because the CLI keeps its login in the Keychain,
     which these variables do not scope. The guarantee above does not depend
     on either.
   - **Docker hub:** the run is
     `docker run --rm --network none <default image>`. The container has no
     route anywhere, so nothing can leave it, whatever the CLI tries.
   - **Bare hub:** the base URL is a loopback port the hub holds open for the
     run's lifetime and never reads or answers. Holding the port means no
     other local process can be listening there. Nothing is logged, because
     nothing is read.

   This is stricter than the stub #334 proposed. A stub that answers receives
   the request, and would have to promise not to log headers that, under the
   operator's login, carry a real token. Here there is no listener that reads
   anything, and on Linux no token to send.

3. **It runs the CLI the loops run.** The resolver uses the hub's default
   runtime: the default workstation image on a docker hub, the host's `claude`
   on a bare one. The id shown is then the one a new loop gets. A loop with an
   image of its own may run a different CLI; its `resolved_model` (#330) says
   what it actually ran on.

4. **When it runs: at hub start, and when a custom model entry is added.**
   - At hub start the four family aliases are resolved in the background.
     Start never waits on the resolver.
   - An entry added to the custom model list (#332) is resolved when it is
     saved.
   - Nothing resolves per request. The API serves what is stored.
   - A resolution is stored with the CLI version it came from. An observed
     `resolved_model` from a real turn on the same alias replaces it, but only
     from a loop on the default runtime and image, since that is the CLI item 3
     resolves with. Such a turn ran under the operator's login, so it is the
     more authoritative answer, and it catches a change between hub starts. A
     loop on an image of its own shows its own `resolved_model` and leaves the
     stored resolution alone.

5. **Failure is bounded and shows the bare alias.** Each run is killed at init
   or after 30 seconds, whichever comes first. The 30 seconds cover a cold
   `docker run`. A run that yields no init is logged and leaves that alias
   unresolved: the alias shows bare until the next start, or until a turn
   observed as item 4 allows resolves it. A failed resolution never blocks
   start, loop creation or a model edit.

6. **Auxiliary runs are a named kind.** The CLI is now invoked in three ways:
   - a loop's turns (ADR-0001);
   - `claude --version` at preflight;
   - alias resolution.

   The last two are auxiliary runs. Both, and any future auxiliary run, follow
   these rules:
   - it holds no credential;
   - it reaches no API;
   - it is bounded in time;
   - it never runs per request.

   The host `claude --version` breaks the first rule today: it inherits the
   hub's environment, including any token the hub was started with, though it
   uses none. The implementation of this ADR builds its environment as item 2
   does, so that the rule holds for every member and the next auxiliary run
   has a compliant one to copy. The docker preflight's `--version` already
   passes no environment.

   An auxiliary run that needs the operator's login, or a real answer from the
   API, is not one of these and needs its own ADR.

## Consequences

- Every alias shows its id from the first start of a hub, at no cost in
  tokens. This rests on two observed CLI behaviours: local resolution, and
  init before the first request. Both are recorded in ADR-0001's CLI
  behaviour section as of 2.1.282. A CLI that moved resolution server-side
  would make init report the bare alias or nothing. The resolver treats that
  as unresolved, so the failure is a bare alias, not a wrong id.
- A resolution without a login could in principle differ from a logged-in
  one, for example if aliases ever resolved per plan. Observed turns (item 4)
  correct the shown id as soon as a loop on the default runtime and image runs
  on the alias.
- A hub start costs four short CLI runs, in parallel and in the background:
  about a second each on the host, plus container start on docker.
- The tier-2 suite models this in `cmd/fakeclaude`:
  - aliases resolve to fixed ids at init, citing this ADR's measurements in the
    fidelity ledger (#168);
  - the fake records the environment it was started with, so a test can assert
    that no credential variable reached the run;
  - the test asserts the id is read from init and the process is killed before
    any turn is recorded.
