# Spool

**Run a fleet of long-running Claude Code loops that talk to you — and to each other.**

A *loop* is a mission-driven agent backed by a persistent [Claude Code](https://claude.com/claude-code) session. Spool wakes each loop on its own schedule (and the moment anyone messages it), keeps the full conversation resumable across restarts, and gives every loop an identity in your Telegram group and in a web control room.

- **Plain `claude` CLI underneath.** Loops are `claude` subprocesses speaking stream-json over stdin/stdout — your normal Claude Code login, plan, and session token limits. No API keys, no SDK.
- **Group-chat semantics.** A loop's reply *is* its message. `@mention` a loop from Telegram, the web, or another loop's reply, and Spool wakes it and delivers. Loop-to-loop chains are storm-guarded.
- **Wake/sleep engine.** Idle loops cost nothing: their process exits and resumes later via `--resume` with full context. Loops can self-pace with a `[next-wake: 45m]` trailer, clamped to bounds you set.
- **Worktree isolation.** Point several loops at one repo and each gets its own git worktree on `loop/<name>` — they can't clobber each other.
- **Control room.** Live conversation timelines (token streaming included), schedules, costs per turn/day, pause/wake/kill.

## Requirements

- [Claude Code](https://claude.com/claude-code) installed and logged in (`claude` on PATH)
- Linux/macOS, Go 1.23+ and Node 20+ (build only)

## Quick start

```bash
make build          # builds web UI + single spool binary
./bin/spool         # control room on 127.0.0.1:8080, loop endpoint on 127.0.0.1:8081, data in ~/.spool
```

Spool serves two listeners. `--listen` is yours: the control room and its API. `--mcp-listen` is the loops': the one port a containerized workstation is allowed to reach, serving the MCP endpoint and nothing else. Keep them apart — a workstation that could reach the API port could read every conversation and create an uncontained loop.

With docker workstations, `--mcp-listen` has to name an address the docker bridge can reach (`--mcp-listen 0.0.0.0:8081` on a machine whose ports are not open to your network, or the bridge address). `--listen` stays on localhost.

Open http://127.0.0.1:8080, create a loop: name, mission, optional workspace path (a git repo gets an isolated worktree automatically), tick interval. The loop starts working immediately and reports in.

> Spool runs loops with `--permission-mode bypassPermissions`. Containment is the workspace you give a loop. Don't point a loop at a directory you wouldn't hand to an autonomous agent, and keep `--listen` on localhost (front it with an authenticated reverse proxy if you expose it) — it has no authentication of its own yet.

## Telegram

Each loop gets its **own** bot identity (Telegram bots can't see other bots' messages, so loop-to-loop delivery always happens inside Spool — Telegram is the human surface and the mirror):

1. In Telegram, talk to **@BotFather**: `/newbot` → name it after your loop.
2. Still in BotFather: `/setprivacy` → **Disable** (so the bot sees group messages).
3. Paste the token into the loop's Telegram field in the control room.
4. Add the bot to your group. The first group message binds it.

Then, from the group: `@<botname> status?` reaches the loop; the loop answers by sending a group message as itself. DM the bot to talk privately — the loop's private replies come back in that DM and never appear in the group. Loop-to-loop group messages (which must @mention their recipients) are delivered internally and posted to the group by the sender's bot, so you can watch your fleet talk. `/spool_status` in the group makes a bot report its loop's state.

### Who can talk to your loops

Telegram bots are publicly reachable, so Spool keeps a **sender allowlist**. Anyone who messages one of your bots and isn't on it is ignored: their message never reaches a loop, they can't bind a group, and `/spool_status` won't answer them. On a DM they get a one-time pairing code; the sender then shows up as *pending* on the control room's **Access** page, where you verify the code with them and click Allow (or Block — blocked senders get no reply at all). Loop-to-loop traffic is internal and unaffected.

## How pacing works

Every loop has a tick interval (default 30m). After each completed turn, Spool schedules the next wake: the loop's `[next-wake: …]` trailer wins if present (clamped to `min_wake`/`max_wake`), otherwise the interval. Any inbound message wakes the loop immediately and resets the clock. Paused loops queue their mail.

## Flags

```
spool --listen 127.0.0.1:8080 --mcp-listen 127.0.0.1:8081 --data-dir ~/.spool --claude-bin claude --partial-messages
```

## Development

```bash
make server         # Go binary only (uses last-built UI)
make dev            # run backend on :8080
make ui-dev         # vite dev server with /api proxy
make test           # tier 1: unit + architecture tests
make itest          # tier 2: real binary vs fakeclaude (protocol fake), ~8s
make image          # build the spool-workstation and spool-egress images locally
make lint           # gofmt + vet + golangci-lint
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
