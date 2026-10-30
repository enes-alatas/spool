# ADR-0061: A cloud loop starts from its GitHub repo, in an environment of its own

Date: 2026-10-28 · Status: accepted (operator decision of 2026-10-29 on #1180, comment 6498693769: the flow as written, with multi-repo loops; the operator decision of 2026-10-29 on #1185, comment 6498931340: the deny rules and the `add_repo` guard of item 2, with a dedicated GitHub user recommended, not required; the operator decision of 2026-10-28 on #1168: the setup-script route, with the token as an environment variable; the network secret in its place is Terra's correction of the same day on #1180, comment 6478145150, agreed by Elliot in 6478162314, which narrows that posture; and the operator decision of 2026-10-29 on #1180, comment 6493335622, that a kill revokes the token for good) · Amended: 2026-10-29 (the `SessionStart` answer's shape, #1181); 2026-10-30 (`/mcp` takes the handshake between turns, #1181); 2026-10-30 (item 6: the setup check, #1182) · Supersedes: the operator's park of #975's hardened mode (option A, 2026-10-28), which this route builds as the only mode; ADR-0059 item 1 (that the hub doesn't read the TUI, and the binding's "only on that start's token"), item 2 (the loop home a start bundles), item 4 (a fresh hub token per session, in the VM), item 6 (one environment for every cloud loop) and its #978 amendment's single stored choice, item 8's #1094 amendment (`CCR_ENABLE_BUNDLE`), item 10's *The repo field goes* and its #1169 amendment, and item 9's #977 amendment's create refusal · Relates to: ADR-0022, ADR-0028, ADR-0043, ADR-0053, ADR-0056, ADR-0058, ADR-0059, ADR-0060

## Context

ADR-0059 starts a cloud loop's session from a *loop home*: a git repo
the hub builds for each start, with the hub's MCP server, the loop's
hooks and its prompt, which the CLI bundles and uploads. In an
Anthropic-hosted environment, every GitHub request goes through
Anthropic's GitHub proxy, which serves only the repositories attached to
the session. A bundled start attaches none, so a cloud loop reaches no
repository, whatever token it holds (#1168, ADR-0059 item 10's #1169
amendment).

A session attaches a repository only when it starts from one: the CLI
reads the working directory's `origin` and sends it as the session's
source, which Anthropic clones through Claude's GitHub App. A session
started that way gets no bundle, so the hub's files never reach it. The
operator chose, on 2026-10-28, to put them in the session another way:
through the *setup script* of a claude.ai environment, one environment
per cloud loop (#1168, comment 6472755748).

What is proven (issue 1168 and #957), on CLI 2.1.310 unless noted:

- **The setup script runs as root before the session**, about one second
  before `SessionStart`, in every run. Hooks it merges into
  `/root/.claude/settings.json` fire, and an MCP server it writes into
  `/root/.claude.json` is loaded at user scope (comment 6469183361).
- **The script doesn't see the environment's variables, but the session
  does**, and `${VAR}` in a user-level MCP header is expanded at session
  time (same comment).
- **A network secret reaches the hub without entering the VM** (ADR-0059
  run C, CLI 2.1.304): set on the environment's claude.ai page for a
  host, it makes the environment's proxy add `Authorization: Bearer …` to
  MCP and plain HTTP requests for that host. Only the claude.ai pages set
  one.
- **`--settings '{"remote":{"defaultEnvironmentId":"env_…"}}'` picks the
  environment per start** (ADR-0059 run F, again in comment 6472296154).
  `--environment` takes only self-hosted (`ccpool_…`) environments and
  refuses an `env_…` ID.
- **The CLI sends one repository as the source**: the working directory's
  `origin` on GitHub. Without `--ref` it bundles instead when the current
  branch has no history on the remote, and an empty start directory
  has none (run R). `--ref <branch>` makes it start from that branch on
  GitHub. The flag works but `claude --help` doesn't list it.

Runs R, S, M and M2, recorded on #1180 (comments 6493263390,
6498672655 and 6498946352), proved the rest on the session's side. A
real hub behind the allowlist is checked at #1181's first live start:

- **Run R: a start from a repo source in a chosen environment, from item
  2's start directory itself** (an empty git repo whose `origin` is the
  GitHub repo, on an unborn branch), with `--ref`. The session cloned the
  repo through the App in the chosen environment, its setup script ran
  before `SessionStart`, and the CLI printed `View:
  https://claude.ai/code/session_…`. A push from the session made a new
  branch, authored and committed as `Claude <noreply@anthropic.com>`.
- **Run S: the session's side of the hub**, against a logging MCP server
  in the VM and a synthetic network secret for an echo host, since
  today's hub can't show a loop's token to paste:
  - The proxy adds the secret to the hooks' requests, not to the setup
    script's.
  - A user-level MCP server whose host isn't on the environment's
    allowlist isn't loaded at all.
  - `${CLAUDE_CODE_REMOTE_SESSION_ID}` in the MCP header arrives
    expanded, in the `cse_` form.
  - The MCP client connects (discover, initialize, a GET for a stream,
    `tools/list`) before `SessionStart` fires. It took a 405 on the GET
    and carried on.
  - It made no call in a 234-second gap between turns, and the next
    turn's tool call worked with no new handshake.
  - After a reclaim, the next turn ran on a new machine: files were
    carried over, the setup script didn't run again, `SessionStart` fired
    with `source: resume`, and the client connected again as that turn
    began.

  What a real hub adds is its public host's reach through the
  allowlist, which ADR-0059's cloud loops already prove, and the
  network secret on MCP requests, which ADR-0059's run C proved.

- **Run M: more repos from inside the session** (#1180, comment
  6498672655). A session started from one repo called `add_repo` for a
  second with push access, with no approval prompt, and then `gh api`,
  `git ls-remote`, a clone of the second repo and a push of a new branch
  to it worked. `list_repos` listed every repository the account's
  GitHub connection can push to.
- **Run M2: no `add_repo` without a GitHub source** (comment
  6498946352). A start from a local repo with no remote, holding a
  file of its own, bundled. The session saw the file, but it had no
  `mcp_config` and so no `claude-code-remote` server, and `gh api` and a
  push to a repo it named were refused. The repo tools come with a
  GitHub source, so a loop can't start from the hub's files and attach
  every repo.

## Decision

1. **Each cloud loop has a claude.ai environment of its own, which the
   operator creates by hand.** Spool never creates or edits an
   environment: the only way to do that is an endpoint Anthropic doesn't
   publish. The environment holds:
   - Spool's **setup script** (item 3);
   - the loop's **hub token as a network secret** for the hub's public
     host (item 4);
   - network access *Custom*, with the hub's public host and the loop's
     allowlist (item 6).

   The hub stores the environment's ID on the loop. It is picked by name
   from the loop's Claude login's environments, the one read-only listing
   ADR-0059 item 11's #978 amendment keeps, or pasted when the list fails.
   The hub checks the ID's shape before it goes into `--settings`, and
   builds that JSON with an encoder, never from strings.

2. **A cloud loop names its GitHub repos, and each session starts from
   the first and attaches the rest.**
   - The loop's repo is `owner/name` on github.com, with a branch,
     `main` until the operator changes it, set at New loop and editable
     while the loop is stopped. The branch is required because the start
     names it (below), and the hub can't read a private repo's default
     branch.
     Both are checked when they are set and again at each start:
     `owner` and `name` hold only the characters GitHub allows, and the
     branch passes `git check-ref-format --branch` and doesn't start with
     `-`.
   - The hub starts each session by running `claude --cloud` under a
     pseudo-terminal (ADR-0059 item 1) in a *start directory*: a fresh
     0700 directory holding an empty git repo whose `origin` is the
     loop's repo, on the loop's branch. It holds no file of the hub's and
     no clone, and the hub removes it when the start ends. The hub builds
     it with `git` run by argument list, never through a shell.
   - The start passes `--settings` with the loop's environment (item 1)
     and `--ref` with the loop's branch, and doesn't set
     `CCR_ENABLE_BUNDLE`, so nothing is uploaded. Without `--ref` the
     CLI would try to bundle the empty start directory, and fail (run R).
   - `--ref` isn't in `claude --help`. If a CLI release drops it, the
     start fails before any session exists and the loop is held with a
     problem (ADR-0053), never started from a bundle. A tier-2 test holds
     the argument list.
   - The CLI sends one repo as the source. A loop may also name more
     repos, `owner/name` each, checked as the first is. The session
     attaches them itself: the hub's prompt (item 5) lists them and tells
     the loop to call `add_repo`, a tool of the `claude-code-remote` MCP
     server that the sessions of runs R, S and M had, all started from
     a repo, then clone each one. Run M did this without an approval
     prompt. The tool is
     undocumented, so a loop whose `add_repo` call fails says so in its
     reply rather than failing quietly, and the first repo still works.
   - The session's GitHub reach is Claude's GitHub App, through the
     claude.ai account's connection: REST through `gh api` and pushes to a
     branch, never GraphQL, a tag or a branch deletion (#1169). It is the
     same identity for every cloud loop on that login, and it covers every
     repository the installation covers, not only the loop's. A loop
     can attach any of those with `add_repo`, not only the ones it
     names: `list_repos` in run M listed every repository the account's
     GitHub connection can push to, across owners, and Spool has no
     list of its own that binds the loop. So the bound is the reach of
     the GitHub user behind the claude.ai account, on every owner, not
     the loop's list, and it takes in any account- or org-wide grant
     made for other purposes. The setup steps tell the operator to
     install the App on selected repositories only, and to review that
     user's installations at `github.com/settings/installations`. The
     recommended setup is a loop login (ADR-0060) whose GitHub
     connection is a dedicated GitHub user, a collaborator only on the
     cloud loops' repos; the operator's own account is the accepted
     alternative. GitHub shows a push's commits as authored and
     committed by `Claude <noreply@anthropic.com>` (runs R and M), so a
     loop's commits name no loop, and the loop's own commit trailers are
     what tell them apart. Commits keep that default identity, with no
     per-loop author and no Spool co-author: the operator decision of
     2026-10-29 (#1180, comment 6498693769).
   - The setup script also writes a `PreToolUse` hook on
     `mcp__claude-code-remote__add_repo`. It posts the call's input to
     the hub's `/hooks`, and the hub refuses any repo the loop doesn't
     name, and logs the refusal with the loop. It takes the post only
     from the loop's recorded session, as `/mcp` is bound (item 4). A
     hook that can't reach the hub refuses. This is a guard against a
     careless or injected call, not a boundary: a shell in the VM runs
     as root and can remove the hook, and the reach above stays the
     bound.
   - The same `claude-code-remote` server gives the session tools that
     act on the claude.ai account. The CLI sets the session's
     `mcp_config` itself, so a start through it can't leave them out, as
     #861 left them out of sessions made through the API. Run M read
     each tool's policy from that `mcp_config`:
     - `always_allow`: `list_sessions`, `get_session`, `list_events`,
       `list_triggers`, `list_environments`, `send_later` and
       `list_repos`;
     - no policy, so the session's permission check decides:
       `send_message`, `create_session`, `archive_session`,
       `create_trigger`, `fire_trigger`, `register_repo_root` and
       `add_repo`.
     Run M2's session, with no server behind them, also named
     `update_trigger`, `delete_trigger` and `check_repo_access` in its
     `allowed_tools`.
   - Two of their reaches go past the one trust domain #919 accepted
     (ADR-0056), that a cloud loop may act as any other cloud loop:
     - **Acting after a kill.** `create_trigger` makes a routine that
       runs on a schedule outside any loop, with the account's GitHub
       reach, and `fire_trigger` runs one at once. A session made by
       `create_session` or a message sent by `send_later` outlives the
       turn too. Stop, kill and Rotate token act on the loop's hub
       token, so they end none of these, and kill is the button for a
       loop that misbehaved (item 4).
     - **Reading beyond the fleet.** `list_sessions`, `get_session` and
       `list_events` read any session on the account. On the default
       login that account is the operator's own claude.ai, so the
       sessions are theirs too, not only cloud loops', and their
       transcripts can hold anything.
   - So the setup script writes `permissions.deny` rules, each as
     `mcp__claude-code-remote__<tool>`, into
     `/root/.claude/settings.json`, where a resumed machine keeps them,
     for every tool runs M and M2 saw but `add_repo`,
     `register_repo_root` and `list_repos`, which this item needs. A
     tool a later CLI adds isn't denied until it is named. The
     start's `--settings` carries the same rules, but whether those
     reach a cloud session is unproven, so the design rests on the setup
     script's copy. A root shell in the VM can edit the rules away, so
     they too are a guard against a careless or injected call.
     The deny rules and the `add_repo` guard are the operator decision
     of 2026-10-29 (#1185, comment 6498931340), which also keeps the
     dedicated GitHub user recommended, not required.
   - #1181's first live start proves a call to each denied tool is
     refused, the `always_allow` ones included. If a deny rule doesn't
     beat `always_allow`, the reads stay open, and #1181 records each
     such tool as a limit by name. Cloud loops would then need a
     claude.ai login of their own (ADR-0060), never the operator's
     personal one, and the setup steps would name the risk of the
     personal login: its own claude.ai sessions are readable by every
     cloud loop on it. That is the operator's call.
   - THREAT-MODEL.md's *Cloud session* boundary records this with
     #1181: a loop reaches every repo of the account's GitHub user and
     the account's sessions and routines unless the deny rules hold;
     Spool's stop, kill and rotate don't end a routine or session a loop
     made; and after killing a loop that misbehaved, the operator checks
     claude.ai's routines and sessions.
   - A cloud loop's `env-var` connections no longer carry its GitHub
     reach. A `GH_TOKEN` connection is refused through the proxy for any
     repository the session didn't start from or attach, as #1169
     found. So #1166
     (a fresh GitHub App token for cloud loops) has nothing left to fix.

3. **The setup script connects the session to the hub, and carries no
   secret.** It is the same text for every cloud loop of a hub. The hub
   serves it on the loop's setup view (#1182) as fixed text, for the
   operator to read before pasting, and it fetches nothing at run time.
   It writes:
   - the hub's cloud listener as the MCP server `spool` in
     `/root/.claude.json`, by URL, with the session-ID header of item 4
     and no token;
   - the `SessionStart` and `Stop` hooks of ADR-0059 item 2, merged into
     `/root/.claude/settings.json`, each a `sh` and `curl` command to the
     hub's public host with no token.
   - the `PreToolUse` guard on `add_repo` and the `permissions.deny`
     rules of item 2, in the same file.

   The network secret adds the token to both (item 4). `spool` loads
   only if the hub's public host is on the environment's allowlist (run
   S), so the allowlist text of item 6 always carries it. The script
   runs once per machine, before the first `SessionStart`. A machine
   resumed after a reclaim keeps its files and doesn't run the script
   again (run S), so the script starts no process the session relies
   on. The script changes only with the hub's public address, and a
   change shows on every cloud loop's setup view, to paste again.

4. **The loop's hub token is a network secret, long-lived, and refused
   while the loop isn't running.**
   - The hub mints the token when the loop's setup view first asks for
     it, shows it once, and keeps only its hash, as for every other
     token. Backups carry no usable token.
   - The token never enters the VM: no file, variable or bundle holds it
     (ADR-0059's hardened mode, now the only mode). So it can't be copied
     out of a session, but anything running in the VM can still use it:
     the proxy adds it to every request for the hub's host, whoever makes
     it. The rules below bound that use.
   - The token reaches what a session token reaches today: the cloud
     listener's `/mcp` and `/hooks` for its own loop, never another loop.
     The control room's API refuses a loop's hub token outright, on any
     host, and a test holds both. That matters when the cloud listener
     shares the control room's name (OPERATIONS.md, #1154): the proxy then
     adds the token to every request for that name, the control room's
     paths included. A name of the cloud listener's own stays the
     recommended setup, and the setup view says why.
   - The hub accepts the token only while its loop runs. A stopped or
     paused loop's token is refused on every cloud-listener route, at
     once, as ADR-0059 item 5 stops a loop, and starting the loop again
     makes the same token valid, with nothing to paste. A kill revokes
     the token for good, since kill is the button for a loop that
     misbehaved: the loop starts again only after Rotate token and the
     new token set as the environment's secret, and the loop's page says
     so. This is the operator decision of 2026-10-29 on #1180 (comment
     6493335622), on the question of comment 6475976600.
   - Within a running loop, `/mcp` takes the token only while a turn is
     in flight: from the hub's send until the turn ends (ADR-0059 item
     5's #1028 amendment), and for one minute after. A long-running loop
     spends most of its life between wakes, and a process left running in
     the VM is the likeliest misuse then. A call refused this way is
     logged with the loop, so a turn the hub wrongly thinks ended shows
     up rather than failing quietly.
   - In run S, the client called `/mcp` only inside a turn: it connected
     as the session started or resumed, which is inside the turn the hub's
     send began, and it made no call in a quiet gap of nearly four
     minutes. After that gap, the next turn's tool call worked on the
     same connection. The client also opens a GET for a server stream at
     connect. The cloud listener's `/mcp` answers that GET with 405, as
     the run's server did and the client carried on, so no stream stays
     open across the end of a turn. If a later CLI is seen calling
     between turns, the bound widens to take those calls, or is dropped
     and the gap recorded as a limit of the one trust domain (ADR-0056)
     if they can't be told apart from misuse. The session binding below
     holds either way.
   - **Rotate token**, on the loop's page, mints a new token, refuses the
     old one at once and shows the new one once, for the operator to set
     as the environment's secret. Deleting the loop revokes its token
     for good.
   - The token belongs to the environment, not to one session. So every
     live session in that environment reaches the hub as the loop, an
     old one that a rotation (ADR-0022) left behind included.
   - So ADR-0059 item 1's binding loses its last clause, "only on that
     start's token": an old session in the environment holds the same
     token, and could post `SessionStart` with its own ID inside a
     pending start's window and take the binding. In its place, the hub
     reads the new session's ID from the CLI's own output under the pty:
     the CLI prints `View: https://claude.ai/code/session_…` once it has
     created the session, as the run of comment 6472296154 printed (its
     output is quoted in comment 6483144237). A `SessionStart` post
     records the session only if its ID is that one,
     in either of its forms, and still only while the start is pending,
     once, within its window. The hook doesn't send a refused post again, so
     a post that arrives before the hub has read the `View:` line is
     held, not refused, until the line is read or the window ends, and
     is judged then. A post naming any other ID is refused and
     logged, as is one naming a session that was once the loop's. The
     ID a post names is `$CLAUDE_CODE_REMOTE_SESSION_ID`, as ADR-0059's
     hook sends it, never the hook input's `session_id`, which is the
     CLI's transcript ID (run S). Run R saw the `View:` line printed on a
     start from a repo too.
   - A `Stop` post from any session but the loop's recorded one is
     refused and logged.
   - A session someone starts by hand on claude.ai in the loop's
     environment gets the token too. Its `SessionStart` and `Stop` posts
     are refused, but its `/mcp` calls would act as the loop. So the setup
     script also sends the session's ID on every MCP request, as a header
     whose value is `${CLAUDE_CODE_REMOTE_SESSION_ID}`, expanded at
     session time as the probe found `${VAR}` expanded (comment
     6469183361). `/mcp` then takes only the loop's recorded session, in
     either of the ID's forms (ADR-0059 item 1's #1099 amendment). A
     process in the VM can forge that header, but a session started by
     hand carries its own ID. Run S saw the header arrive expanded, in
     the `cse_` form.
   - The client connects to `/mcp` before `SessionStart` fires (run S),
     so it can't wait for the binding. While a start is pending, `/mcp`
     also takes the ID the hub read from the `View:` line. A call that
     comes before the hub has read that line is held, as an early
     `SessionStart` post is, until the line is read or the start's
     window ends.

   **Amendment (2026-10-30, #1181):** the bound widens to take the
   handshake, as this item foresaw for a CLI seen calling between turns.
   In the first live checks (#1190, comment 6526275151), the session's
   client reconnected to `/mcp` between turns several times. Opening the
   session's page and running `/permissions` or `/compact` there caused
   some of these. Each reconnect was refused with a 401, and the client
   then kept `spool` failed into the next turn, which made no new request
   and reached nobody. So `/mcp` now takes any request from the loop's
   own session, at any time: the handshake and the tool list. Only a tool
   call outside a turn and its minute is refused, and it is refused as a
   tool error naming the reason, not a 401, so the client stays
   connected. The tool list names nothing the setup script doesn't. A
   session that isn't the loop's is still refused with a 401, as before.

5. **The hub's words reach the session through the `SessionStart`
   answer.** The loop home's `CLAUDE.md` carried the system prompt, and
   there is no loop home now. The repo's own `CLAUDE.md` loads as a
   contained loop's checkout's does.
   - The hub answers the start's `SessionStart` post with a JSON body:
     the loop's `env-var` connections as ADR-0059 item 10 builds them,
     and the prompt from `internal/loop/prompt.go`, which names the
     loop's other repos for it to attach (item 2).
   - The hook appends the connections to `CLAUDE_ENV_FILE` and prints the
     prompt as `hookSpecificOutput.additionalContext`, which Claude Code
     adds to the session's context. A refused post prints nothing.
   - The prompt is context, not a system prompt, as the loop home's
     `CLAUDE.md` was. Unlike `CLAUDE.md`, context added once at startup
     isn't reloaded after a compaction or a `/clear`. So the hook's
     matcher widens from ADR-0059 item 10's `startup` to `startup`,
     `compact` and `clear`, and the hub answers a `compact` or `clear`
     post with the prompt alone, never the connections, which the env
     file keeps. It takes such a post only from the loop's recorded
     session (in either ID form) and only while a turn is in flight, as
     `/mcp` is bound. Any other source is still refused. A machine
     resumed after a reclaim fires `SessionStart` with `source: resume`
     (run S), which the matcher leaves out: the resumed session keeps
     its context.
   - #1181 checks that a loop follows the prompt as it followed the loop
     home's, on its first turn and on a turn after a compaction.
   - The answer stays the one route the response redactor exempts
     (ADR-0059 item 10).

   **Amendment (2026-10-29, #1181):** the answer isn't one JSON body.
   Its first line is the connections' `export` lines as one base64
   line, empty for a `compact` or `clear` answer; the rest is the
   hook's output JSON, whose `additionalContext` is the prompt. The
   hook appends the decoded first line to `CLAUDE_ENV_FILE` and prints
   the rest, so it needs only `sh`, `sed`, `base64` and `curl`, and no
   JSON parser in the VM. Both ends are Spool's own, the hook script and
   the hub.

6. **Each loop's allowlist is its environment's, confirmed per loop.**
   The hub's confirm step of ADR-0059 item 6 moves from Settings → Cloud
   to each loop's setup view, with its rules unchanged: the text to paste
   is built per request from the hub's egress allowlist and the public
   host, a confirm names the SHA-256 of the text it confirmed, and a loop
   whose text isn't confirmed carries the warning that the hub's
   allowlist doesn't bind it.

   **Amendment (2026-10-30, #1182):** the setup view also checks the
   setup, as Consequences foresaw: the hub can't read the environment
   back, only see whether a start reaches it.
   - An admin's check of a running loop starts one session from the
     loop's repo in its environment, as a start does, with a fixed message
     from `prompt.go` that asks nothing of it. It passes on a
     `SessionStart` post naming the session the CLI printed, held for the
     `View:` line as a start's post is (item 4).
   - The check's session is never the loop's. Its post is answered with
     no connection and no prompt, `/mcp` refuses it, and its `Stop` post
     is taken and dropped. It stays on claude.ai, as every session does.
   - A failed check names its stage: `environment` (the CLI refused the
     start), `token` (a post or an MCP request naming the session was
     refused for its token) or `unreached` (nothing of the session came
     within the start's bound).
   - The loop is held with the `cloud_setup` problem until a check passes
     on its repo, branch, environment and hub token as they are now,
     against the address that last passed the hub's own check. An edit,
     a Rotate token or a kill takes a new check, and so does a new public
     address. A check against an earlier address counts again if that
     address comes back.
   - A confirm made on Settings → Cloud no longer warns or clears any
     loop. That step goes with the control room's slice (#1183).

7. **A cloud loop runs on its own Claude login's account** (ADR-0060).
   - Its environment must belong to the claude.ai account of the loop's
     login. The hub lists environments on that login's token and stores
     an ID from that list.
   - A pasted ID, taken when the list fails (item 1), is checked against
     the list once it can be read. For a loop on the default login it is
     used meanwhile, as ADR-0059 item 6's #978 amendment uses one. For a
     loop on any other login, the loop doesn't wake until the list
     confirms the ID.
   - The start runs under that login's token, as ADR-0059 item 8 runs it
     under the default's. #1078 lifts the 409 of #1071 on this rule.
   - So a loop on a non-default login never starts in another account's
     environment. The default login's pasted ID is the one unverified
     case, as it is today, and #1078 records what the CLI answers to an ID
     its account doesn't list.

8. **Settings → Cloud loses its environment step.** The shared
   environment, its stored ID and its allowlist confirm go. Settings →
   Cloud keeps the public address and its check, and the account check.
   A cloud loop is created once the public address checks green. Its
   setup view then walks the rest, and the loop wakes only once its
   environment and repo are set.

9. **Existing cloud loops move at their next start.** A cloud loop made
   under ADR-0059 has no repo and no environment of its own. Once #1181
   lands, it is held with a problem (ADR-0053) that names the setup to
   finish, and wakes again once both are set. Its per-session token is
   revoked on that boot, since nothing can use it.

10. **It ships in slices** under #1179. Each slice updates the docs of
    record its change touches in the same PR: OPERATIONS.md,
    THREAT-MODEL.md's *Cloud session* and *Cloud listener* boundaries,
    and ARCHITECTURE.md's runtime row.
    1. this ADR, with runs R and S (#1180);
    2. the loop's repo and environment, and the start from them (#1181);
    3. the setup view: the script, the token, rotate, the allowlist
       confirm, and the check (#1182);
    4. the control room (#1183);
    5. OPERATIONS.md's *Cloud loops* told once as one story in order,
       from creating the environment to the loop's first reply, as
       ADR-0059 item 12's slice 3 told it (#1184);
    6. a cloud loop on a login other than the default (#1078).

## Runs

Each run is recorded on #1180 with its evidence (comments 6493263390,
6498672655 and 6498946352), on CLI 2.1.310 under the operator's
sign-in, in throwaway environments since archived.

- **Run R** (done): a start from item 2's start directory (an empty repo
  with an `origin` and an unborn branch), in a chosen environment. It
  bundled and failed without `--ref`, and started from GitHub with it.
- **Run S** (done, narrowed): the session's side of the hub, against a
  logging MCP server in the VM and a synthetic network secret for an
  echo host, not a real hub. Today's hub stores a loop's token sealed
  and can't show one to paste, and its public host's reach is already
  ADR-0059's. The first live start of #1181 against a real hub is the
  check that remains, and its PR records it.
- **Run M** (done): a second repo attached from inside a session with
  `add_repo`, read through `gh api`, cloned, and pushed to as a new
  branch.
- **Run M2** (done): a start from a local repo with no remote, which
  bundled and got no `claude-code-remote` server, so no `add_repo`.

## Alternatives

- **Keep the bundled start** (ADR-0059). Everything is automatic, but a
  cloud loop reaches no repository.
- **The token as an environment variable, with `${VAR}` in the setup
  script's files.** Proven to work (comment 6469183361), but the
  session's shell can read the token, and anyone with access to the
  environment on the account can too. The network secret keeps it out of
  the VM, at the same cost to the operator.
- **A one-off routine per session** (operator idea of 2026-10-28): the
  hub has the CLI create a routine that runs at once with the loop's
  repos, then sends into its session. It could attach several repos, but
  a routine's session still runs in an environment, so the token's place
  is unchanged. The routine is created through `/schedule`, a skill whose
  tool needs a model turn and the CLI's classifier review, on the wake
  path. Whether a routine takes several repos or per-loop variables is
  unproven.
- **The environments API**, so Spool creates each loop's environment.
  Setup stays automatic, but the API is unpublished and can't set a
  network secret (ADR-0056 item 5's amendment).
- **The sessions API with a source and a bundle** (#1168, comment
  6467741001). REST access worked and the bundle's hooks ran, but it is
  the unpublished surface ADR-0059 left.
- **A start from the hub's files, attaching every repo with
  `add_repo`** (operator question of 2026-10-29). The hub's files would
  ride in the bundle, but a bundled session has no `claude-code-remote`
  server, so no `add_repo` (run M2).
- **The hub clones the repo into the loop home.** The bundle is still
  what the session starts from, so the proxy still attaches nothing.

## Consequences

- **A cloud loop can do GitHub work on its repos** through Claude's GitHub
  App, with that App's limits: no GraphQL, no tags, no branch deletion,
  and the reach of the GitHub user behind the account, not only the
  loop's repos. A guard holds `add_repo` to the loop's repos, but a
  root shell in the VM can remove it.
- **Setup is by hand, per loop:** an environment on claude.ai with the
  script, the secret and the allowlist, and the App on the repo. Spool
  shows each value to paste and can't check the environment itself, only
  that a start reaches the hub.
- **The hub token is long-lived and stored at Anthropic** as the
  environment's secret. The hub refuses it while the loop isn't running,
  and Rotate token replaces it. Only the operator can remove it from
  claude.ai.
- **The token never enters the VM.** A copy can't be lifted out of a
  session. Any live session in the loop's environment can use it, on
  `/mcp` only as the loop's recorded session and only during a turn.
- **No hub file is uploaded,** so nothing of the hub's is at rest at
  Anthropic but what the session itself writes.
- **The start leans on `--ref`, a flag `claude --help` doesn't list.** A
  CLI release that drops it stops cloud loops from starting, loudly,
  until the start finds another way to name the branch.
- **A routine or session a loop made outlives its kill** unless the
  deny rules of item 2 held. After killing a loop that misbehaved, the
  operator checks claude.ai's routines and sessions.
- **A loop can read the account's sessions** through `list_sessions`,
  `get_session` and `list_events` unless the deny rules of item 2 beat
  their `always_allow`. On the default login those are the operator's
  own claude.ai sessions.
- **A change of the hub's public address** means pasting the setup
  script again into every cloud loop's environment.
- **The prompt reaches a cloud loop as hook context,** as it reached it
  as `CLAUDE.md` before.
