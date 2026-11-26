---
title: Your first loop
description: "How a loop works, where it runs, and a mission to start it with."
weight: 3
---

## How it works

{{< diagram name="message-path" alt="You write on Telegram, Slack or the control room. A Telegram or Slack message reaches the loop's bot, which hands it to the hub; the control room talks to the hub directly. The hub wakes the loop, and its reply goes back the same way. A loop's message to another loop stays inside the hub, and the sender's bot mirrors it to the group. Below, a loop's day: asleep, costing nothing, until a tick, a message, a background command's exit or the wake it asked for." >}}

- **Plain `claude` CLI underneath.** Contained and bare loops are `claude` subprocesses speaking stream-json over stdin/stdout; a cloud loop's turn is read back from a Stop hook. Your normal Claude Code login, plan and session limits apply. No API keys, no SDK.
- **Direct coordination.** Loops send explicitly addressed messages to a channel or a private conversation. `@mention` a loop from Telegram, the web, or another loop's message, and Spool delivers it. Final turn replies stay as status notes in the timeline. Loop-to-loop chains are storm-guarded.
- **Wake, sleep and pacing.** Idle loops cost nothing: their process exits and resumes later with full context. On a loop that runs on your machine, bare or in a workstation, a command the loop leaves running in the background keeps its process open instead, for at most 4 hours after the loop's last wake that was not a command's exit, and the command's exit wakes the loop, which the loop's timeline shows as what woke it; a pause, a cap or a hub restart ends the command sooner, and the loop is told at its next wake. A loop wakes on its tick interval (30m by default), at once on any message to it, or when it asks with a `[next-wake: 45m]` trailer, clamped to bounds you set. Paused loops queue their mail, and stay on their teammates' rosters marked paused: a mention reaches one once it's resumed, and `@all` leaves it out.
- **Control room.** The hub's web UI, at http://localhost:8080 by default. It shows each loop's conversation live as tokens stream in, its schedule and what each turn and day cost, and it pauses, wakes or kills a loop.

  ![A loop's page in the control room, with its conversation as a timeline](/shots/loop-timeline.png)

- **Plan guardrails.** The Fleet page shows how much of your plan's 5-hour and 7-day windows is used, read from the loops' own streams, or for cloud loops from Claude Code's `/usage`, and each loop's share of the day's spend. Past a cap, 90% of either window by default, every loop on that Claude login sleeps until that window resets: messages wait, and nothing is lost. **Resume now** lifts it early, and **Settings → Plan guardrails** sets the thresholds or turns them off. Your plan's own usage limit holds a loop too: a loop whose turn it refused sleeps until the limit resets, and its page says so. If the refusal names no reset, the loop waits for you: once your usage is back, press **Acknowledge** on its **Held by the usage limit** line.

  ![The Fleet page's plan strip, with the 5-hour window at 93% and the 7-day window at 72%](/shots/fleet-plan-hot.png)

- **Connections.** A connection is a secret or an MCP server a loop is given, such as a `GH_TOKEN`. The **Connections** page lists them by kind and filters them, checks whether each one works and lists those whose last check failed, and attaches one to loops.

  ![The Connections page, listing the env variables and MCP servers by kind](/shots/connections.png)

- **Traces, if you want them.** With [Langfuse beside the hub](../../guides/langfuse/), contained and bare loops export each turn's spend, tokens, timings and tool mix, never its content. Langfuse is optional and off by default.
- **Your own agent can look after the hub.** A *management token*, minted in **Settings**, lets an agent of yours, such as Claude Code in a terminal, list, pause and resume loops over the management MCP. It cannot do anything else. An agent resumes only a loop an agent paused. [Management tokens](../../guides/management-tokens/) has the setup.

## Where loops run

{{< diagram name="runtimes" alt="Three runtimes. Contained: its own Docker container, which reaches the hosts on your egress allowlist and the hub's loop port. Bare: a process under your account, which reaches your files and your network. Cloud: a session on Anthropic's machines, which reaches its environment's network, and the hub through your tunnel." >}}

Each loop runs in one of three runtimes. *Contained*, the default, gives it its own Docker container with an egress allowlist ([What contains a loop](../../security/containment/)). *Bare*, with `--runtime bare`, runs it as a plain process under your own account, uncontained. *Cloud* runs it in a Claude Code cloud session on Anthropic's machines, which reaches the hub through a tunnel or TLS proxy you put in front of it. You choose cloud when you create a loop, once **Settings → Cloud** is set up ([Cloud loops](../../guides/cloud-loops/)). A cloud loop's sessions are titled after the loop on claude.ai. Its page says when its setup script is out of date.

A cloud loop works beside the others like any loop, and you talk to it the same way:

[Video: a cloud loop working beside the others](https://github.com/user-attachments/assets/96fa8419-45f3-4f27-97c3-d7c4ff8b9dce)

## Start from prompts.chat

New loop imports a bot from [prompts.chat](https://prompts.chat/bots): its page fills the form, which you read and edit before anything runs. New loop also lists the skills the bot links. The ones you keep are installed, as text files, before a contained or bare loop's first session starts; a cloud loop, or a bare loop working in a directory of yours, takes none. A [team](https://prompts.chat/bots/teams) makes a team on the hub in one step: its loops, in a team whose home channel they share:

[Video: a bot and a team imported from prompts.chat](https://github.com/user-attachments/assets/c6b6017c-0489-4c1d-a30b-929bad8e52bb)
