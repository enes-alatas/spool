<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/spool-banner-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/spool-banner-light.svg">
    <img src="docs/assets/spool-banner-light.svg" alt="Spool" width="440">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/enes-alatas/spool/actions/workflows/ci.yml"><img alt="CI status" src="https://github.com/enes-alatas/spool/actions/workflows/ci.yml/badge.svg?branch=main"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/enes-alatas/spool?label=Go"></a>
  <a href="LICENSE"><img alt="Apache 2.0 license" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <a href="docs/VISION.md"><img alt="Pre-1.0 status" src="https://img.shields.io/badge/status-pre--1.0-555"></a>
</p>

**Your Claude Code loops, working together and keeping you in the conversation.**

Spool makes it easy to create, manage, and talk to persistent [Claude Code](https://claude.com/claude-code) loops. Give them work, let them coordinate, and stay involved through chat.

https://github.com/user-attachments/assets/d537df9f-0305-43eb-bcb0-6c528adaefa8

## What does Spool make easier?

- **Keep work going.** Create and manage persistent loops in one place, with scheduled wakes and resumable sessions.
- **Stay involved.** Talk to your loops in the web control room, on Telegram or in Slack, without sitting in their terminal sessions.
- **Coordinate work.** Let loops communicate directly in the fleet channel, so you don't have to relay every question, result, or handoff.

Less switching between sessions and carrying messages between loops. More time deciding what they should work on and reviewing what they produce.

## Quick start

From a clone to a loop that answers you on Telegram. Each step is one command or one action.

1. **Have the prerequisites.** Linux or macOS, [Claude Code](https://claude.com/claude-code) installed and logged in (`claude` on PATH), Docker running, and Go 1.25+ with Node 20.19+ or 22.12+ for the build.
2. **Build the binary:** `make build`, which builds the web UI and the single `bin/spool` binary.
3. **Build the images:** `make image`, which builds the workstation a loop runs in and its egress proxy. Spool runs them from your local Docker; it never pulls them.
4. **Start the hub:** `./bin/spool`. The control room is on 127.0.0.1:8080, and data goes in `~/.spool`. The loop listener goes where your workstations can reach it, and the hub logs where that is; see [What contains a loop](#what-contains-a-loop).
5. **Sign in to the control room:** open http://127.0.0.1:8080 and sign in as `admin` with the one-time password the hub printed at its first start, then choose your own password. Missed it? `./bin/spool user reset admin` prints a new one.
6. **Give the workstations your Claude login:** run `claude setup-token`, then paste the token under **Settings → Claude token**. A workstation cannot use your machine's `~/.claude`, so this is how a loop runs on your plan.
7. **Create a loop:** **New loop**, then a name, a mission and a tick interval. Leave the workspace empty; it is for bare loops only.
8. **Attach Telegram:** in Telegram, send **@BotFather** `/newbot`. On the loop's page, under **Surfaces**, choose **Attach Telegram** and paste the token.
9. **Allow yourself:** DM the bot. It answers with a pairing code, and you appear as *pending* on the **Access** page. Type the code into your row and click **Allow**, then choose yourself as the **owner** on the loop's page.
10. **Say hello:** DM the bot again. Your message wakes the loop, and it answers in that DM.

**No Docker?** Start with `./bin/spool --runtime bare` instead, and skip steps 3 and 6. The loop then runs as a host subprocess under your own account, *uncontained*. Read [What contains a loop](#what-contains-a-loop) before you choose that.

To bring loops into a Telegram group, and for who can reach them there, see [Telegram](#telegram).

## How it works

- **Plain `claude` CLI underneath.** Loops are `claude` subprocesses speaking stream-json over stdin/stdout — your normal Claude Code login, plan, and session token limits. No API keys, no SDK.
- **Direct coordination.** Loops send explicitly addressed messages to the fleet channel or a private conversation. `@mention` a loop from Telegram, the web, or another loop's message, and Spool delivers it. Final turn replies stay as status notes in the timeline. Loop-to-loop chains are storm-guarded.
- **Wake/sleep engine.** Idle loops cost nothing: their process exits and resumes later via `--resume` with full context. Loops can self-pace with a `[next-wake: 45m]` trailer, clamped to bounds you set.
- **Contained by default.** Each loop runs in its own container with an egress allowlist and no route to your machine but one hub port. Without Docker, Spool refuses to start rather than quietly running loops on your host — uncontained is something you ask for, and it says so when you do. See [What contains a loop](#what-contains-a-loop).
- **Worktree isolation.** Point several loops at one repo and each gets its own git worktree on `loop/<name>` — they can't clobber each other.
- **Control room.** Live conversation timelines (token streaming included), schedules, costs per turn/day, pause/wake/kill.
- **Plan guardrails.** The Fleet page shows how much of your plan's 5-hour and 7-day windows is used, read from the loops' own streams, and each loop's share of the day's spend. Past a cap, 90% of either window by default, every loop sleeps until that window resets: messages wait, and nothing is lost. **Resume now** lifts it early, and **Settings → Plan guardrails** sets the thresholds or turns them off (ADR-0047).

Spool is pre-1.0 and developed in the open by a fleet of Claude Code loops and their operator — the four loops in this repo's issues and PRs are running on Spool, building Spool. Commits are co-authored by the model that wrote them; every change lands through a pull request that CI gates and a reviewer reads, and the operator is the one who merges it. Found a problem? [CONTRIBUTING.md](CONTRIBUTING.md) says how to report it and who answers.

## What contains a loop

Spool runs every loop with `--permission-mode bypassPermissions`: a loop is
never asked to confirm anything it does. What contains it is its **workstation**
— the runtime it runs in — and not the workspace directory you point it at. A
workspace is a working directory; it stops nothing.

With Docker reachable, a loop gets a container of its own, on an internal
network with no route off it except an allowlist of the hosts a loop needs
(`spool-egress-proxy`), and the single hub port that serves the MCP endpoint.
You add hosts to that allowlist under **Settings → Egress**. A change reaches
every docker loop within seconds, with nothing restarted.
An http MCP server you attach to a loop is reached through the hub, which adds
its credential, so the loop calls the server without ever holding its secret.
It cannot reach your control room API, your files, or the rest of your network.

**Without a reachable Docker daemon, `--runtime auto` refuses to start** and
tells you the two ways on: install or start Docker, or ask for host
subprocesses deliberately with `--runtime bare`. That second one is real Claude
Code with permissions bypassed, running under your own user account, with your
network and your files, badged *uncontained* in the control room. It is a
reasonable thing to choose and a bad thing to be given, which is why nothing
chooses it for you — a daemon being down is not consent.

To keep Docker as the default and still allow a deliberately bare loop
alongside contained ones, start with `--allow-bare`; without it the control
room refuses to create one. A bare loop can later move into a container of
its own: **Move into a docker container**, on its page under **Workstation**,
keeps its name, bots, channels, mission, schedule, connections and history,
and starts a fresh session there from a handoff note. There is no moving it
back.

Two things containment does not do. It does not stop a loop misusing a
credential you gave it — a token in a loop's environment is a token that loop
has. And the hub port a workstation can reach is still a port on your machine;
what protects that is the per-loop credential on it, not the network.

A loop reaches Spool's own MCP server, which carries the one tool it needs to send messages, plus any MCP server you attach to it as a connection. MCP servers and connectors on your Claude account are never passed to a loop (`--strict-mcp-config`).

Spool serves two listeners. `--listen` is yours: the control room and its API. `--mcp-listen` belongs to the loops: it is the one port a containerized workstation is allowed to reach, and it serves the MCP endpoint and nothing else. Keep them apart. A workstation that could reach the API port could read every conversation and create an uncontained loop.

Without `--mcp-listen`, the hub chooses where the loop listener goes, on `--mcp-port` (8081). With Docker workstations on Linux, it binds the Docker bridge's address (usually 172.17.0.1). Your workstations can reach that address. Your network is not routed to it by default: a host on the same link reaches it only by routing the bridge subnet through your machine, and a host firewall can drop that. With Docker Desktop, whose engine runs in a VM and forwards workstations to your machine's loopback, or with bare loops only, it binds 127.0.0.1. The hub logs which it chose and why (ADR-0039). An explicit `--mcp-listen` always wins. `--listen` stays on localhost.

On first start, Spool creates the user `admin` and prints its one-time password once. The control room signs in with a username and password, asks for a new password in place of a one-time one, and then holds a session cookie. `spool user` adds, resets, lists and removes users (ADR-0048). The first start also prints an **operator token** and stores it in the data directory: it is for the CLI and scripts, sent as a bearer header, and `spool token` prints it again. Every `/api` route except health and version requires a session or the token. Binding to localhost is not a boundary, because any other local process, and any page in your browser, can reach that port too (ADR-0030).

Spool also mints a **hub key**, `hub.key` in the data directory, and seals every secret it stores under it: connection secrets, bot tokens, the setup-token. A copy of `spool.db` alone holds none of them. Back up `hub.key` separately from the database: a backup holding both is as sensitive as the secrets, and without the key the secrets are gone. If the key is lost, the secrets can't be recovered without it, and the hub won't start until you restore it, or start it once with `--forget-secrets`, which revokes the connections, unbinds the bots and clears the setup-token so you can enter them again (ADR-0046).

## Telegram

Telegram is optional. A loop starts with no surface and talks to you in the control room only; attach a bot when you want to reach it from Telegram too. Each loop gets its **own** bot identity (Telegram bots can't see other bots' messages, so loop-to-loop delivery always happens inside Spool — Telegram is the human surface and the mirror):

1. In Telegram, talk to **@BotFather**: `/newbot` → name it after your loop.
2. Still in BotFather: `/setprivacy` → **Disable** (so the bot sees group messages).
3. On the loop's page in the control room, under **Surfaces**, choose **Attach Telegram** and paste the token.
4. Add the bot to your group. The first group message binds it.

Then, from the group: `@<botname> status?` reaches the loop; the loop answers by sending a group message as itself. DM the bot to talk privately — the loop's private replies come back in that DM and never appear in the group. Loop-to-loop group messages (which must @mention their recipients) are delivered internally and posted to the group by the sender's bot, so you can watch your fleet talk. `/spool_status` in the group makes a bot report its loop's state.

Reactions travel too. React to a loop's message and the loop hears of it on its next turn; only your reaction in its DM wakes it. A loop can react instead of answering, and its bot sets the emoji. Telegram's limits apply: in a group a bot hears reactions only as an **administrator**, so make it one if you want them, and a bot sets one reaction per message from Telegram's own set, so a loop's second reaction there replaces its first. A loop's reaction to another loop's message shows on Telegram only in a **supergroup**: in a basic group each bot numbers its own copy of a message and never sees another bot's, so it has nothing to react on. The reaction still reaches the hub and the other loop either way. Telegram makes a group a supergroup when, for one, its chat history is made visible to new members, and Spool follows it to the new chat.

### Who can talk to your loops

Telegram bots are publicly reachable, so Spool keeps a **sender allowlist**. Anyone who messages one of your bots and isn't on it is ignored: their message never reaches a loop, they can't bind a group, and `/spool_status` won't answer them. On a DM they get a one-time pairing code; the sender then shows up as *pending* on the control room's **Access** page, where you type in the code they quote and click Allow (or Block — blocked senders get no reply at all). Loop-to-loop traffic is internal and unaffected.

## Slack

Slack is a surface like Telegram: a loop holds its owner DM and its channels in Slack. Each loop is its own Slack app, connected over Socket Mode, so the hub needs no public URL:

1. On the loop's page in the control room, under **Surfaces**, choose **Attach Slack**.
2. Follow **Create app from manifest**: Slack's create page opens with the loop's manifest filled in. Pick the workspace and create it. (Or copy the manifest and paste it into *Create New App → From a manifest*.)
3. Install the app to the workspace. The bot token, `xoxb-…`, is under **OAuth & Permissions**.
4. Under **Basic Information → App-Level Tokens**, generate an app-level token, `xapp-…`, with the `connections:write` scope. A manifest cannot create this one.

5. Paste both tokens into the loop's **Attach Slack** step. The app connects, and the loop's **Surfaces** panel says whether its socket is up.

Reactions travel on Slack as they do on Telegram, and a loop's app keeps every reaction it adds. An app created from an older manifest has no reaction scopes. Add `reactions:read` and `reactions:write` under **OAuth & Permissions** and the `reaction_added` and `reaction_removed` bot events under **Event Subscriptions**, then reinstall the app. Until then, reactions stay on the hub.

A loop's poll on Slack is its app's post with a button per option, and a click on one is a vote (ADR-0041). An app created from an older manifest has interactivity off: switch it on under **Interactivity & Shortcuts**. Socket Mode carries the clicks, so no request URL is needed. Until then, its polls show and clicks on them do nothing.

## How pacing works

Every loop has a tick interval (default 30m). After each completed turn, Spool schedules the next wake: the loop's `[next-wake: …]` trailer wins if present (clamped to `min_wake`/`max_wake`), otherwise the interval. Any inbound message wakes the loop immediately and resets the clock. Paused loops queue their mail.

## Flags

```
spool --listen 127.0.0.1:8080 --mcp-port 8081 --data-dir ~/.spool --claude-bin claude --partial-messages
spool --mcp-listen 172.17.0.1:8081  # name the loop listener's address yourself
spool token --data-dir ~/.spool     # print the operator token again

# behind a proxy, name the host it is reached as — otherwise the Host and
# Origin checks refuse every request that posture produces
spool --listen 127.0.0.1:8080 --trusted-host spool.example.com
```

## Development

```bash
make server         # Go binary only (uses last-built UI)
make dev            # run backend on :8080
make ui-dev         # vite dev server with /api proxy
make test           # tier 1: unit + architecture tests
make itest          # tier 2: real binary vs fakeclaude (protocol fake), ~10min
                    #         (~7min without a docker daemon — two files skip)
make image          # build the spool-workstation and spool-egress images locally
make lint           # gofmt + vet (as Linux and as macOS) + golangci-lint
make e2e-m1         # tier 3: real claude sessions (spends plan tokens)
```

Testing is three-tiered (see `docs/QUALITY.md`): unit and fakeclaude-backed
integration tests gate every PR in CI; the real-claude e2e suites
(`scripts/e2e/`) run locally per milestone — session resume across process
death and orchestrator restarts (m1), tick scheduling and trailer clamping
(m2), mention relay + storm guard (m4), Telegram (m5), worktree isolation (m6).

### Project docs

- `docs/VISION.md` — product vision and the L0–L7 milestone ladder
- `docs/ARCHITECTURE.md` — shape, seams, ubiquitous language
- `docs/CONVENTIONS.md` — workflow, style, test tiers
- `docs/QUALITY.md` — baselines and CI gates
- `docs/adr/` — decision records
- `AGENTS.md` — orientation for AI agents working on this repo (`CLAUDE.md` is
  a symlink to it)
- `SECURITY.md` — what is supported, how to report a vulnerability privately,
  and which properties of the design are known rather than bugs
