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
- **Stay involved.** Talk to your loops in the web control room or through Telegram, without sitting in their terminal sessions.
- **Coordinate work.** Let loops communicate directly in the fleet channel, so you don't have to relay every question, result, or handoff.

Less switching between sessions and carrying messages between loops. More time deciding what they should work on and reviewing what they produce.

## Quick start

From a clone to a loop that answers you on Telegram. Each step is one command or one action.

1. **Have the prerequisites.** Linux or macOS, [Claude Code](https://claude.com/claude-code) installed and logged in (`claude` on PATH), Docker running, and Go 1.25+ with Node 20.19+ or 22.12+ for the build.
2. **Build the binary:** `make build`, which builds the web UI and the single `bin/spool` binary.
3. **Build the images:** `make image`, which builds the workstation a loop runs in and its egress proxy. Spool runs them from your local Docker; it never pulls them.
4. **Start the hub:** `./bin/spool`. The control room is on 127.0.0.1:8080, and data goes in `~/.spool`. The loop listener goes where your workstations can reach it, and the hub logs where that is; see [What contains a loop](#what-contains-a-loop).
5. **Log in to the control room:** open http://127.0.0.1:8080 and paste the operator token the hub printed. `./bin/spool token` prints it again.
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

Spool is pre-1.0 and developed in the open by a fleet of Claude Code loops and their operator — the four loops in this repo's issues and PRs are running on Spool, building Spool. Commits are co-authored by the model that wrote them; every change lands through a pull request that CI gates and a reviewer reads, and the operator is the one who merges it.

## What contains a loop

Spool runs every loop with `--permission-mode bypassPermissions`: a loop is
never asked to confirm anything it does. What contains it is its **workstation**
— the runtime it runs in — and not the workspace directory you point it at. A
workspace is a working directory; it stops nothing.

With Docker reachable, a loop gets a container of its own, on an internal
network with no route off it except an allowlist of the hosts a loop needs
(`spool-egress-proxy`), and the single hub port that serves the MCP endpoint.
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
room refuses to create one.

Two things containment does not do. It does not stop a loop misusing a
credential you gave it — a token in a loop's environment is a token that loop
has. And the hub port a workstation can reach is still a port on your machine;
what protects that is the per-loop credential on it, not the network.

A loop reaches only Spool's own MCP server, which carries the one tool it needs to send messages. MCP servers and connectors on your Claude account are never passed to a loop (`--strict-mcp-config`).

Spool serves two listeners. `--listen` is yours: the control room and its API. `--mcp-listen` belongs to the loops: it is the one port a containerized workstation is allowed to reach, and it serves the MCP endpoint and nothing else. Keep them apart. A workstation that could reach the API port could read every conversation and create an uncontained loop.

Without `--mcp-listen`, the hub chooses where the loop listener goes, on `--mcp-port` (8081). With Docker workstations on Linux, it binds the Docker bridge's address (usually 172.17.0.1). Your workstations can reach that address. Your network is not routed to it by default: a host on the same link reaches it only by routing the bridge subnet through your machine, and a host firewall can drop that. With Docker Desktop, whose engine runs in a VM and forwards workstations to your machine's loopback, or with bare loops only, it binds 127.0.0.1. The hub logs which it chose and why (ADR-0039). An explicit `--mcp-listen` always wins. `--listen` stays on localhost.

On first start, Spool prints an **operator token** and stores it in the data directory. The control room asks for it once and then holds a session cookie. Every `/api` route except health and version requires it. Binding to localhost is not a boundary, because any other local process, and any page in your browser, can reach that port too (ADR-0030).

## Telegram

Telegram is optional. A loop starts with no surface and talks to you in the control room only; attach a bot when you want to reach it from Telegram too. Each loop gets its **own** bot identity (Telegram bots can't see other bots' messages, so loop-to-loop delivery always happens inside Spool — Telegram is the human surface and the mirror):

1. In Telegram, talk to **@BotFather**: `/newbot` → name it after your loop.
2. Still in BotFather: `/setprivacy` → **Disable** (so the bot sees group messages).
3. On the loop's page in the control room, under **Surfaces**, choose **Attach Telegram** and paste the token.
4. Add the bot to your group. The first group message binds it.

Then, from the group: `@<botname> status?` reaches the loop; the loop answers by sending a group message as itself. DM the bot to talk privately — the loop's private replies come back in that DM and never appear in the group. Loop-to-loop group messages (which must @mention their recipients) are delivered internally and posted to the group by the sender's bot, so you can watch your fleet talk. `/spool_status` in the group makes a bot report its loop's state.

### Who can talk to your loops

Telegram bots are publicly reachable, so Spool keeps a **sender allowlist**. Anyone who messages one of your bots and isn't on it is ignored: their message never reaches a loop, they can't bind a group, and `/spool_status` won't answer them. On a DM they get a one-time pairing code; the sender then shows up as *pending* on the control room's **Access** page, where you type in the code they quote and click Allow (or Block — blocked senders get no reply at all). Loop-to-loop traffic is internal and unaffected.

## Slack

The Slack surface is being built (#230). What works today is creating the app a loop will run as, so it is ready when the surface lands. Each loop is its own Slack app, connected over Socket Mode, so the hub needs no public URL:

1. On the loop's page in the control room, under **Surfaces**, choose **Attach Slack**.
2. Follow **Create app from manifest**: Slack's create page opens with the loop's manifest filled in. Pick the workspace and create it. (Or copy the manifest and paste it into *Create New App → From a manifest*.)
3. Install the app to the workspace. The bot token, `xoxb-…`, is under **OAuth & Permissions**.
4. Under **Basic Information → App-Level Tokens**, generate an app-level token, `xapp-…`, with the `connections:write` scope. A manifest cannot create this one.

Keep both tokens; pasting them into the loop arrives with the surface.

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
