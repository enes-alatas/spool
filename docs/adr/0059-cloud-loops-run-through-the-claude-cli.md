# ADR-0059: Cloud loops run through the claude CLI, on documented surfaces

Date: 2026-10-20 · Status: proposed (operator direction of 2026-10-19 on #957: drive cloud loops through the `claude` CLI, keep the Claude proxy; operator decisions of 2026-10-20 on #957: items 1, 2, 4, 5, 6, 7 and 10) · Amended: 2026-10-21 (item 9: no ADR-0056 loop to move, #977); 2026-10-21 (items 6 and 11: the environment is picked from the account's list, #978) · Supersedes: ADR-0056 item 2 (the sessions client, its amendment's event stream and interrupt), item 3 (the watched read-back), item 4 (for `env-var` connections only, "A cloud loop gets no connections at first"), item 5 (an environment per loop the hub creates and keeps, and for cloud loops the operator decision of 2026-10-15 on #859 that their allowlist is the hub's own), item 6 (the repo a session starts from, and its 2026-10-15 amendment's optional repo field), item 7's amendment (the cap sees cloud turns), and its Alternatives entry *Drive sessions through the `claude` CLI* · Relates to: ADR-0022, ADR-0028, ADR-0047, ADR-0053, ADR-0056, ADR-0058

## Context

ADR-0056 runs a cloud loop from the hub's own Go client for Anthropic's
sessions API. That client creates the loop's environment and session,
sends each wake as an event, polls the session's event stream for the
turn and its cost, interrupts and archives. Most of that API is
undocumented, and ADR-0056 says so: the read-back "may change without
notice", and a change stops every cloud loop until a release of Spool.

ADR-0056 rejected the CLI because, in its words, the CLI "starts no
session without a terminal and reads nothing back". Since then Anthropic
has documented `claude --cloud`. On 2026-10-19 the operator asked to move
cloud loops onto it and keep the Claude proxy (ADR-0058) as it is. The
#957 probes, run on the real account with CLI 2.1.304 between 2026-10-19
and 2026-10-20, found the shape. Each finding is recorded on the issue:

- **A session starts from a local repo with no remote** (run A).
  `claude --cloud "<message>"` bundles a git repo with at least one
  commit and uploads it. The session reads that repo's `.mcp.json`
  servers and its `.claude/settings.json` hooks and permission rules.
- **Starting needs a terminal; sending doesn't** (run A, run D).
  `claude --cloud` refuses without a TTY, and `claude -p "<message>"
  --cloud <id>` sends into an existing session from a script. Sends
  that arrive during a turn join the next one, as a contained loop's
  batching does. `--output-format json` prints only `{ok, session_id,
  url}`, with no reply and no usage.
- **`--permission-mode auto` runs MCP tools unattended** (runs B1–B4).
  `bypassPermissions`, in the flag or in project settings, still
  prompts on claude.ai: the docs say it isn't available in cloud
  sessions. In auto, the session may still ask on claude.ai for some
  actions.
- **A network secret reaches the hub without entering the VM** (run C).
  On the session's environment, it adds `Authorization: Bearer …` to
  MCP and plain HTTP requests for its host, and the value appears in no
  variable or file in the VM. A secret belongs to an environment, and
  only the claude.ai pages create one.
- **The repo's hooks run inside the VM, on every turn** (runs E4–E6).
  `Stop` fires after each turn, `-p` follow-ups included, with the
  transcript's path at `/root/.claude/projects/<p>/<session>.jsonl`. The
  last assistant line carries the model, the reply and the full token
  usage, and no dollar cost and no rate-limit event. A hook reaches only
  the hosts the environment allows: runs E–E3 posted to a host outside
  the allowlist, and failed with no trace.
- **`--settings` picks the environment per call** (run F):
  `{"remote":{"defaultEnvironmentId":"env_…"}}` starts the session there
  and leaves the user default alone. The CLI can't create or edit an
  environment.
- **A send doesn't refresh the login** far from its expiry (run G).
- **A send runs on a token passed in `CLAUDE_CODE_OAUTH_TOKEN`** with no
  credential in the config dir, once that dir's `.claude.json` names the
  account's organization (runs H and H2).
- **The CLI can't interrupt or archive a cloud session.** Those are
  buttons on claude.ai. An archived session refuses a send with
  `{ok:false, …}`.

## Decision

1. **The hub starts a session by running `claude --cloud` under a
   pseudo-terminal** (operator decision of 2026-10-20). The first wake is
   its message, with `--permission-mode auto` and the environment chosen
   through `--settings`.
   - The hub doesn't read the TUI. The loop home's `SessionStart` hook
     posts `CLAUDE_CODE_REMOTE_SESSION_ID` to the hub, which records it
     as the loop's session and ends the local process. The hub accepts
     that post only while a start of that loop is pending, within its
     two-minute window, once per start, and only on that start's token.
     At any other time it is refused, but for item 10's re-delivery on
     resume, which records nothing. The loop can read its token and
     run curl, so without this it could rebind its own wakes into
     another session and send the messages meant for it there.
   - Before each start, the hub writes into its CLI config dir the
     onboarding-done flag and that start's loop home's folder trust
     (`projects["<path>"].hasTrustDialogAccepted`). The operator met the
     trust prompt on these runs, and a fresh config dir opens a theme
     picker.
   - A start that reports no session within two minutes fails, with the
     CLI's last line, and the loop shows the error. Errors in a row open
     ADR-0053's `turn_errors` problem.
   - Context rotation (ADR-0022) starts a new session the same way.

2. **Each start bundles a *loop home*: a small git repo the hub builds
   for that start alone, in a fresh 0700 directory outside the data
   directory, with one commit.** The hub removes it once the start ends,
   when `SessionStart` has posted or the start has failed. So the
   session's token is never at rest on the hub's disk past the start, and
   no backup carries it. The hub keeps only the token's hash, as it does
   for every other token. The commit holds:
   - `.mcp.json`, registering the hub's cloud listener as `spool`, with
     the loop's hub token in its `Authorization` header (item 4);
   - `.claude/settings.json`, with the two hooks below. Each one's
     command is `sh` and `curl`, which the VM has;
   - `CLAUDE.md`, carrying the system prompt a contained loop gets
     through `--append-system-prompt`. Its text is built in
     `internal/loop/prompt.go` and nowhere else. Nothing documents
     `claude --cloud` carrying `--append-system-prompt` into the session,
     so the project memory file is the carrier. The model reads it as
     project instructions rather than as system prompt, and the first
     slice checks that the loop follows it as a contained loop follows
     its prompt.

   The hooks post to the cloud listener with the loop's hub token, as
   MCP calls do:
   - `SessionStart` posts the session's ID (item 1);
   - `Stop` posts the turn's new transcript lines, from the line after
     the last one it sent.

   The cloud listener gains one route for these, beside `/mcp`, under
   the same bounds and the same token check (operator decision of
   2026-10-20). Posting them as a tool call through `/mcp` would put the
   tool in the model's own tool list, offering it a call meant only for
   its harness.

3. **A wake is `claude -p "<envelope>" --cloud <session>`**, run by the
   hub with no terminal, and a sleep does nothing, as in ADR-0056.
   - The runner reads the turn from what the `Stop` hook posts. The hub
     turns the transcript lines into the stream-json events the runner
     already reads, and ends the turn on the hook's post. The hook's own
     `last_assistant_message` is the turn's final text.
   - A send the CLI refuses, an archived session included, fails the
     turn with the CLI's error.
   - A prompt edited while a session runs reaches it as a contained
     loop's does: `reconcilePrompt` (#162) puts the
     standing-instructions note into the next wake's envelope, with no
     rotation. The loop home's
     `CLAUDE.md` carries the prompt as it was at the session's start,
     and the next start writes the current one.
   - A turn whose `Stop` post never comes ends as an error after 2 hours
     with no post, ADR-0056 item 3's backstop. The session's status
     can't be read without the sessions API, so the backstop is the only
     bound.
   - What the hook posts is untrusted input: it carries text the loop
     wrote. It is bounded in size and line count, redacted as a
     contained loop's turn is, and a post over its bound ends the turn
     as an error.
   - The `Stop` post is the loop's own word, so it drives nothing
     privileged. It ends the turn, and it gives the turn's text, its
     events for display and the cost estimate, and nothing else. The hub
     takes no session ID, rotation signal, permission or tool result from
     it. The loop can forge its own token counts. That is acceptable,
     since the cost is an estimate and the cap doesn't see cloud turns
     (item 7).

4. **The hub token is in the VM, a fresh one per session** (operator
   decision of 2026-10-20).
   - The hub mints a token for each session it starts, writes it into
     that start's loop home's `.mcp.json` (item 2), and revokes it when
     the loop stops or the session is replaced. A copy lifted out of a
     session dies with that session.
   - The loop can read its own token, as ADR-0056 item 5's amendment
     already has it (`SPOOL_HUB_TOKEN`). It reaches only that loop's
     `/mcp` and hook route on the cloud listener, and cloud loops are
     already one trust domain (ADR-0056, #919). What a token in the VM
     adds is a copy usable from elsewhere until its session ends. The
     operator weighed it as the posture of any MCP client, and narrower
     than most.
   - The bundle is uploaded to Anthropic and kept with the session, so
     its token is too. That is the same place the VM is, and the same
     lifetime. The hub's own copy is gone with the loop home (item 2).
   - **The hardened mode** is the one run C proved: an environment per
     loop, set up by the operator on claude.ai, with the loop's token as
     a network secret for the hub's host and none in `.mcp.json`. The
     token never enters the VM. Building it as a per-loop option is out
     of this ADR's slices. It gets its own issue when this ADR is
     accepted, and until then the hardened mode is documented, not built.

5. **Stop revokes at once** (operator decision of 2026-10-20). Stopping
   or deleting a cloud loop revokes its session's token and sends
   nothing more. The hub doesn't wait for a turn in flight: an operator
   stopping a misbehaving loop must cut it off now.
   - The CLI can't interrupt, so a turn already running goes on in the
     VM until it ends, cut off from the hub but with its other tools.
     So can a turn about to run: a wake sent just before the stop joins
     the next turn (run D), which may start after it. The same holds for
     a session that rotation replaces. A turn counts as running from the
     hub's send until its `Stop` post, so the hub already knows it. The
     loop's page says so for a turn running or about to run, and links
     the session on claude.ai, where the operator can interrupt and
     archive it. The kill button is the same stop, with the same
     sentence.
   - The VM idles, pauses and is reclaimed by Anthropic, as ADR-0056's
     sleep already relies on.

6. **One environment for every cloud loop, which the operator sets up on
   claude.ai once.** The operator pastes its `env_…` ID into Settings,
   and the hub passes it on each start (item 1). The environment must let
   the session reach the hub's public host. It holds network settings
   only: every cloud loop shares it, so a variable set there reaches
   them all. A loop's secrets come from the hub (item 10), and the setup
   steps say to put none in the environment.

   **Holding it to the hub's allowlist is optional, with a warning**
   (operator decision of 2026-10-20). The page shows the list to paste:
   network access *Custom*, with the hub's egress allowlist (ADR-0028's
   built-in list and the operator's stored hosts) plus the hub's public
   host. Nothing else can set it:
   - no settings key or CLI flag sets an environment's allowed hosts. The
     docs say "No server-managed setting adds domains to an environment's
     network allowlist", and only the environment's dialog edits it. An
     edit there reaches running sessions within about a minute;
   - the Bash sandbox (`sandbox.network.allowedDomains` in the loop home's
     settings) only narrows, covers shell commands only (not WebFetch,
     MCP or hooks), ignores its strict mode when a repo sets it, and is
     untested in the VM. It is no substitute.

   The hub can't read the environment back. Until the operator confirms
   they pasted the current list, and again after any edit to the
   allowlist in Settings, cloud loops carry a warning: the hub's egress
   allowlist does not bind them, only what their environment allows. A
   stale list shows otherwise only as a cloud loop's failed fetch.
   ADR-0056 item 8 already says that Anthropic's proxy, not our wall,
   enforces a cloud loop's allowlist. To keep the warning load-bearing:
   - the confirmation also resets when the pasted `env_…` ID changes;
   - the hub serves the text to paste, the public host included, built
     from the current allowlist on each request and never cached, so the
     page and the hub can't disagree. An entry an environment can't
     carry is left out of it and named beside it;
   - the confirmation is the hub's state, not the page's. A confirm
     records which text it confirmed, and the hub refuses one whose text
     is no longer current;
   - the hub checks the ID's shape before it goes into the `--settings`
     JSON.

   **Amendment (2026-10-21, #978): the environment is picked by name.**
   claude.ai shows no environment ID, and the one documented way to read
   one is the CLI's `/remote-env` picker. So Settings → Cloud lists the
   account's environments by name, with one read-only call on the hub's
   Claude login (item 11's amendment), and the hub stores the ID of the
   one picked, with the name the list gave it (operator decision of
   2026-10-20 on #978). When the list fails, the page asks for the ID,
   read from `remote.defaultEnvironmentId` after a `/remote-env` pick. The
   hub never leaves the choice to the CLI, which takes the first
   environment in the list: that holds only for an account with one. With
   none chosen, no cloud loop is created and none wakes.
   - The list to paste has one host per line, as the environment's
     *Allowed domains* field takes them, the public address's host first.
   - A confirm names the SHA-256 of the text it confirmed. An allowlist
     edit, a new public address or another environment changes the text
     or the ID, and so drops the confirm with no reset of its own.
   - The page counts the setup done only once the list is confirmed
     (operator decision of 2026-10-20 on #979). The hub refuses no loop
     for it: a loop made before an edit runs on, with the warning.

7. **Cost is the hub's estimate, and the cap can't see cloud turns**
   (operator decision of 2026-10-20, keeping that of 2026-10-15 on #872
   that cloud turns report their cost).
   - Each cloud turn records its token counts from the transcript.
   - Its cost is priced from those counts by a table of per-model
     prices in the hub, and the control room shows it as an estimate.
     A model the table lacks shows its cost as unknown. Unknown is not
     zero: the turn's `cost_usd` is null, and a total over it says so.
     Updating the table is part of supporting a new model.
   - The turn's stored cost is marked as estimated, in the store and in
     the API, beside `cost_usd`, which stays per-turn and summable (#194).
     A total that sums estimated and reported costs can then say so.
   - The transcript carries no rate-limit event, so ADR-0056 item 7's
     first text stands again: the plan cap is driven by what contained
     loops observe, it caps cloud loops too, and a hub with only cloud
     loops has unknown usage.

   **Amendment (2026-10-21, #978): how the estimate is served.**
   - The price table is in the hub's `claude` package. It is keyed by
     model ID, and also matches an ID with a snapshot date after it. It
     prices input, output, cache reads, and cache writes; a write the
     usage reports as one-hour is priced at the one-hour rate.
   - The hub prices each API call of the turn, at the model and usage
     its transcript message records, and sums the estimates: a turn may
     change model, and a rate can depend on one call's prompt length.
     A model with a second rate card for long prompts, as
     `claude-haiku-5-5` has above 100K prompt tokens (input, cache reads
     and cache writes), prices a call over that length on it. A call on
     a model the table lacks leaves the turn's cost unknown.
   - A cloud turn's `result` event carries `cost_estimated: true` and
     `turn_cost_usd`, the estimate of this turn alone, and no
     `total_cost_usd`, which would be a session's running total.
   - A turn in the API carries `cost_estimated` beside `cost_usd`. The
     store keeps an unknown cost as 0 with a mark of its own, so a sum
     leaves it out, and the API serves it as null.
   - A loop's day total keeps `cost_today_usd` as the sum of known
     costs. Beside it, `cost_today_estimated` says whether that sum
     includes an estimate, and `cost_today_unknown_turns` counts the
     turns it leaves out.

8. **The hub's Claude login stays sealed.** The hub runs each
   `claude --cloud` and `claude -p --cloud` with the login's current
   access token in `CLAUDE_CODE_OAUTH_TOKEN` and a config dir of its own.
   That dir holds a `.claude.json` with only the account's `oauthAccount`
   block, which the sign-in already reads, and the onboarding and trust
   flags (item 1). It holds no credential, so the login is never at rest
   in the CLI's files, and ADR-0058 items 2 to 4 hold unchanged. Without
   that block the CLI refuses with "Unable to get organization UUID"
   (runs H and H2). The account check (ADR-0056 item 6, step 2) becomes
   ADR-0044's login check on that token, since listing environments is a
   sessions-API call.

   The local CLI can write the first message, which is an envelope, into
   its config dir. So after each run the hub checks that dir holds no
   `.credentials.json`, `projects/`, `shell-snapshots/` or debug log, as
   the sign-in already checks its own dir. Anything it finds there it
   removes and logs. Slice 2 carries both, the clean run and the
   removal, as tests. The CLI's environment is the sign-in's, with the credential
   variables stripped and `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`
   set. The token in that environment is visible only to the hub's own
   user.

9. **Cloud loops made under ADR-0056 move over at their next start.**
   Each has a session the sessions client created, in an environment
   the hub created for that loop alone. Its token is a per-session
   variable of that session (ADR-0056 item 5's amendment), not a network
   secret. Once slice 2 lands:
   - the hub's first boot on slice 2 revokes every ADR-0056 session
     token at once, before any wake. Without the sessions client the hub
     can no longer drive those sessions, so no wake could use the tokens,
     and a loop held for days must not leave a live one behind;
   - the loop's first wake starts a new session through the CLI, in the
     shared environment (item 6), with a fresh token. A loop held while
     no shared environment is set stays held, as a failed address check
     holds it today (ADR-0056 item 6), with its old token already
     revoked;
   - the hub can't archive the old sessions and environments without the
     sessions client, so the Cloud page lists them by name, each session
     with its claude.ai link, for the operator to archive there. Their
     tokens were revoked at that first boot, so nothing in them reaches
     the hub, and the environments hold no secret (ADR-0056 item 5's
     amendment);
   - a loop that has a repo field loses it (item 10). The Cloud page
     lists those loops with the repo each had, so the operator can attach
     a `GH_TOKEN` connection and name the repo in the loop's prompt.

   **Amendment (2026-10-21, #977):** no cloud loop was made under
   ADR-0056 (operator, 2026-10-21), and no release carries the cloud
   runtime, so there is nothing to move. Slice 2 builds none of this
   item but the repo field's removal: the column is dropped, and a
   create that still names a repo is refused, saying to attach a
   `GH_TOKEN` connection and name the repo in the loop's prompt. No
   first-boot revocation and no list on the Cloud page; item 12's
   slice 2 drops them too.

10. **A cloud loop's secrets are its `env-var` connections, which the
    hub hands to each session as it starts** (operator decision of
    2026-10-20). They are the connections a contained loop gets
    (ADR-0043), attached on the same page.
    - The hub answers the `SessionStart` post (item 1) with the loop's
      attached `env-var` connections, as `export` lines in a `text/plain`
      body. The hook writes them to `CLAUDE_ENV_FILE`, so the session's
      shell commands see them as a contained loop's do. Every response
      of the cloud listener goes through the response redactor, and the
      hand-off's writer is the one named exemption: the hub mounts it
      outside `redact.HTTP` on purpose, not because its content type
      happens to slip past. One test holds that the hand-off's values
      arrive unmasked, and another that every other cloud-listener
      route is redacted.
    - `SessionStart` also fires when a reclaimed VM resumes (`source`
      `resume`), after `/clear` and after compaction. The hook writes
      the file only on a 2xx (`curl -sf -o`), so a refusal never
      truncates it or writes an error page into it. It exits 0 and
      prints nothing on either stream, refused or not: a `SessionStart`
      hook's output is added to the model's context.
    - Its matcher is `startup` and `resume`. `clear` and `compact` keep
      the VM and its env file, so they post nothing. Slice 1 first
      checks whether a reclaimed VM keeps the env file. If it does, the
      matcher is `startup` alone and the re-delivery below isn't built.
    - **Re-delivery on resume** is the one answer besides the start's,
      and it is bounded as tightly:
      - only on a post whose `source` is `resume` (or what slice 1 finds
        a reclaim fires), carrying the session's current token and the
        session ID the hub recorded, so it can never rebind the loop to
        another session (item 1);
      - only within two minutes after a hub send to that session, once
        per send. A reclaimed VM wakes because the hub sent it something,
        so the real post falls in that window, and a lifted token reads
        the connections only while the hub is waking that loop, once.
    - **A refused post is loud.** A `SessionStart` from the loop's
      recorded session that the hub refuses raises a problem on the
      loop (ADR-0053), so a loop that has lost its connections is never
      silent.
    - The hub builds each line itself, from the connection's variable
      name and value:
      - the name must match ADR-0043's rule for an `env-var`, checked
        again at hand-off, and a connection that fails it is left out and
        logged;
      - the value is single-quoted, with each `'` written as `'\''`;
      - a value holding a NUL is refused.

      A test round-trips values that hold quotes, newlines, `$()` and
      backticks through `sh`.
    - The start's answer comes only to that post, with its bounds: while
      that loop's start is pending, once, and on that start's token.
      Beyond it, a token lifted out of the VM reads the connections only
      in a resume window, once.
    - So a value is never in the loop home or on the hub's disk outside
      its sealed store, and a lifted hub token can't read it at will. It
      is at rest on the VM's disk at Anthropic, as the bundle is, for
      as long as Anthropic keeps that disk.
    - Deleting a cloud loop leaves those values in its session until the
      operator archives it on claude.ai, and a GitHub token outlives
      that. The delete dialog names the loop's attached connections, to
      rotate if the loop misbehaved.
    - The values are redacted wherever a contained loop's are: the
      `Stop` post (item 3), and every `/mcp` call, such as a
      `send_message`, through the redacting store both listeners write
      through.
    - A change to a connection reaches a cloud loop at its next start,
      not at its next turn as a contained loop's does (ADR-0043 item 5's
      2026-10-07 amendment). The connection's page says so beside each
      attached cloud loop. Stopping and starting the loop applies it now.
    - `mcp-server` connections stay off cloud loops: the brokered servers
      carry organisation credentials, and ADR-0056 item 4 keeps them off
      the internet.
    - The answer crosses whatever makes the cloud listener public. A
      tunnel that ends TLS off the hub's host, as Cloudflare Tunnel does
      at its edge, sees the values in clear, as it sees the hub token.
      THREAT-MODEL.md's *Cloud listener* boundary says so (slice 2).
    - Slice 1 checks that `CLAUDE_ENV_FILE` reaches the shell in the VM:
      on the first wake, on later `-p` sends, after a compaction, and on
      a send into a VM reclaimed after idle. If the values reach none of
      those, the delivery goes back to the operator before slice 2
      builds.

    **The repo field goes.** ADR-0056's session started from a GitHub
    repo through the Claude GitHub App. A CLI session starts from the
    loop home. A cloud loop clones what its prompt names with its own
    `GH_TOKEN` connection, as a contained loop does. Its GitHub reach is
    that token's, not an App installation's, which can cover every repo
    of the organisation. The field and its column (migration 0060) go
    with the sessions client.

11. **What leaves the hub.** The sessions client in
   `internal/runtime/cloud` and its fake, the event-stream read-back,
   the environment and setup-script management with its allowlist sync,
   the interrupt and the archive. Every cloud call goes through the CLI.
   The hub needs the `claude` CLI on its machine for cloud loops, as it
   already does for the sign-in (ADR-0058 item 5).

   **Amendment (2026-10-21, #978): one call stays.** Listing the
   account's environments for item 6's pick is the one call the hub still
   makes to the API the CLI's `/remote-env` reads,
   `GET /v1/environment_providers` (operator decision of 2026-10-20 on
   #978). It is read-only, and made only when Settings → Cloud lists the
   environments or stores a choice. No start or send depends on it: a
   start runs with the stored ID, so a change to that API breaks only the
   pick, and the page falls back to the paste.

12. **It ships in slices**, each with its docs of record
    (ARCHITECTURE.md's runtime row and *Claude login* entry,
    THREAT-MODEL.md's *Cloud session* and *Cloud listener* boundaries):
    1. the loop home, the hook route on the cloud listener and the
       transcript read-back, with a fakeclaude that serves `--cloud` and
       runs the loop home's hooks, for tier 2, and the `CLAUDE_ENV_FILE`
       check of item 10;
    2. starting and sending through the CLI, per-session tokens, stop,
       the connections handed over at start, revoking every ADR-0056
       session token on first boot, and moving the loops made under
       ADR-0056 (item 9). The sessions client and the repo field go;
    3. Settings → Cloud: the shared environment step, its pasted ID and
       allowlist, and the estimated cost in the control room. The setup
       is told once, as one story in order: a *Cloud loops* section in
       OPERATIONS.md walks it from creating the environment on claude.ai
       to the loop's first reply, saying what happens on Spool's side and
       on claude.ai's at each step, and the page's steps follow it in the
       same order and words.

## Alternatives

- **Keep the sessions client** (ADR-0056). It works today, but every
  cloud loop rests on an API Anthropic doesn't publish.
- **Create the session with one sessions-API call and do the rest
  through the CLI.** Simplest code, but it keeps the undocumented
  dependency #957 set out to remove.
- **The operator starts each session by hand** with `claude --cloud`.
  No pty and no undocumented call, but every new loop and every rotation
  needs the operator.
- **One environment per loop, made through the undocumented environments
  API,** with the token as a network secret. The token never enters the
  VM and setup stays automatic, but the API can't set a secret
  (ADR-0056 item 5's amendment), and it is the undocumented surface
  again.
- **Tokens only for cloud turns.** Accurate and nothing to keep up, but
  it drops the #872 decision.
- **The hooks post through `/mcp`**, as a stateless `tools/call`. The
  listener keeps one route, but the model sees the tool and can call it.
- **Secrets as variables of the shared environment.** Nothing for the
  hub to hand over, but every cloud loop gets every secret, and the
  operator keeps them on claude.ai instead of in Spool.
- **Secrets in the loop home's `.claude/settings.json` `env`.** One file
  and no hook. The values would sit at Anthropic as long as the env file
  does, but they would also be in the bundle the hub builds, and on the
  hub's disk until the start ends.
- **Keep the repo field** by cloning the repo into the loop home and
  adding the hub's files as one commit. The session starts in the repo,
  but the hub then needs the loop's GitHub token at start and a clone of
  every cloud loop's repo on its own disk.
- **A soft stop** that waits for the turn's `Stop` before revoking. The
  last turn's replies would land, but a misbehaving loop keeps acting for
  up to a turn.

## Consequences

- **Cloud loops rest on documented surfaces:** the CLI's `--cloud` and
  `-p --cloud`, project hooks, `.mcp.json`, and the transcript file the
  CLI writes. The transcript's line format is the CLI's and not a
  published API. A change there degrades the read-back but not the
  sends, and the hub's parser fails loudly, as ADR-0056 item 3 asks.
- **The hub drives a TUI to start sessions.** A new prompt at start
  hangs the start until the two-minute bound fails it. The loop shows
  the CLI's last line.
- **A cloud loop's hub token is in its VM** for the session's life, and
  in the uploaded bundle. THREAT-MODEL.md's *Cloud session* boundary
  says so, with the hardened mode.
- **Setup gains one manual step:** the shared environment and its ID.
  Pasting the hub's allowlist into it is optional, and cloud loops carry
  a warning until it is pasted and current. The hub's Settings page
  carries both.
- **A cloud loop's connection values are at rest at Anthropic** in its
  VM's disk for as long as Anthropic keeps the session, where a
  contained loop's stay on the operator's machine. They are never in the
  bundle or on the hub's disk outside its sealed store. A changed
  connection reaches the loop at its next start, not its next turn.
- **A cloud loop can't be killed mid-turn from the hub.** It is cut off
  from the hub at once, and the page points to claude.ai for the rest.
- **The plan cap is blind to cloud turns again,** and their cost is an
  estimate.
- **An `auto` session that asks on claude.ai waits there.** Until
  relaying those asks through Spool lands, such a turn sits until item
  3's 2-hour backstop ends it, and the loop's page shows only a busy
  turn. The operator answers on claude.ai, where the session link
  points.
- **Tier 2 needs fakeclaude to play the cloud CLI:** `--cloud` under a
  pty, `-p --cloud`, and the loop home's hooks run against the hub. It
  replaces the fake sessions API.
