# HN outreach shortlist (Milo, 2026-10-27)

Source: HN Algolia comment search, last 60 days, 10 queries (Enes's four + tmux, overnight, phone,
parallel sessions, long-running, multiple agents): 110 unique comments, read in full (notes/hn/all.md).
Rules applied: under ~3 weeks old, the comment itself states the pain, not a competitor's own Show HN
author, reply discloses authorship, links the repo not Enes's own HN post.

## 1. uzairhaq, 2026-10-15, on Launch HN: Agent37
https://news.ycombinator.com/item?id=50111787
Said: Telegram bots in polling mode and Claude Code remote control hold an outbound connection; once a
sandbox sleeps "a message sent from my phone never reaches it". Asks whether a relay outside the
sandbox holds the connection and wakes the agent.
Fit: exactly Spool's shape. The hub holds the Telegram/Slack connection; a loop's claude process runs
only during a turn, woken by a message or its own schedule.
Draft:
> I ran into exactly this and ended up building around it: a small self-hosted hub holds the Telegram/Slack connection, and each Claude Code agent is only a process the hub starts when a message arrives or when the agent's own schedule says so. Between turns the Claude process isn't running, so nothing burns awake hours and nothing is missed. Open source, in case the wake path is useful to look at: https://github.com/enes-alatas/spool (I'm the author.)

## 2. jrejaud, 2026-10-09, on Show HN: talking to my agents from WhatsApp
https://news.ycombinator.com/item?id=50018404
Said: "I do the same thing over Telegram with a forum topic per Claude Code session."
Fit: the one-bot-per-loop model, already hand-built. 18 days old, a thin thread, so the reply may be
seen only by them; still the best-matched person on the list.
Draft:
> Same setup here, and it grew into a tool: each Claude Code session is a Telegram (or Slack) bot of its own, plus a group where they talk to each other and you can watch. The hub runs each one in its own container, keeps it across restarts, and wakes it on a message or on its own timer. Open source, Go: https://github.com/enes-alatas/spool. I built it, so I'd genuinely like to hear what your forum-topic setup does that it doesn't.

## 3. petesergeant, 2026-10-19, on "Tmux and Claude Code: Sessions, Send-Keys, and the Ceiling"
https://news.ycombinator.com/item?id=50150418
Said: tmux + Claude but wants more: sandbox agents from each other and from the disk, partition
secrets, long-running agents on a devbox, a web front end and iOS app for sessions (wrote byre, uses
Happier).
Fit: contained loops (own container, egress allowlist, per-loop connections), long-running, phone
access through chat. They build their own tools, so write to a peer.
Draft:
> If you want the sandboxing part without assembling it: I built Spool, which runs each long-running Claude Code agent in its own container with an egress allowlist and its own secrets, and you talk to each one over Telegram or Slack (so the phone comes for free). The agents can also message each other. Open source: https://github.com/enes-alatas/spool. byre + Happier reads like the same itch from the terminal side, so curious how you find the trade-offs.

## 4. toppa33, 2026-10-19, same tmux thread
https://news.ycombinator.com/item?id=50150090
Said: used tmux + Claude on a Mac mini, gave it up because "there were just too many tmux sessions to
maintain"; herdr shows what's open; excited about Claude Code projects as a project manager across
sessions.
Fit: the maintenance pain and the "what is each one doing" view. Spool's control room shows each
loop's current work and its open problems.
Draft:
> "Too many tmux sessions to maintain" is what made me build Spool: the hub owns the sessions, each one is a container that wakes on a Telegram/Slack message or its own schedule, and one page shows what every agent is doing right now. No tmux, and you talk to them from the phone. Open source: https://github.com/enes-alatas/spool. Author here, so grain of salt.

## 5. afruitpie, 2026-10-14, on OpenCode 2.0
https://news.ycombinator.com/item?id=50095797
Said: wishes Claude Code had a server model for remote hosts; the desktop app "restarts and clobbers
long-running sessions upon reconnect"; Remote Control needs a claude.ai account; is vibe-coding a
macOS app that wraps Claude Code on remote hosts and always lags.
Fit: a hub that owns the sessions so a client reconnect never touches them. Honest caveat in the
reply: Spool also needs a claude.ai login.
Draft:
> For the clobbered-sessions problem I went the other way round: a small self-hosted hub (Go) owns the Claude Code sessions, and the client is just Telegram, Slack or a web page, so a reconnect never touches a session. It does need a claude.ai login, same as Remote Control. Open source, in case it saves you writing the server half: https://github.com/enes-alatas/spool. I'm the author.

## Considered, not shortlisted
- Ask HN "biggest problems with multisession engineering" (49503285, Sep 1) and "how many juggle
  multiple agent sessions" (49469723, Aug 28): perfect topics, 8 weeks old; nobody reads them now.
  Worth watching for the next one of these and answering early.
- ad_fontes (49926588, Oct 1): built a work graph so Claude Code/Codex agents self-coordinate, wants
  human-in-the-loop. Good fit, 4 weeks old; optional sixth if Enes wants it.
- juanre (50199584, Oct 22): 101 agents across three machines with their own coordination; a power
  user who won't switch, but a conversation if Enes wants one.
- spacedcowboy (49509801, Aug 31): overnight runs stopping at "3am"; model behaviour, not tooling.
- vardalab (50155425): wants Codex/Pi/Claude talking to each other; Spool is Claude-only.
- subparsec, dokdev, danielbilekq, gtapps, recurser: authors of neighbouring tools in their own
  threads; replying there reads as a pitch war.

## Claims used in the drafts, verified on main 2026-10-27
own container + egress allowlist (docs/CONTAINMENT.md); Telegram and Slack surfaces; group where
loops message each other; wake on message or own schedule (next-wake trailer, ticks); claude process
runs per turn in contained runtime; boot recovery restarts loops; control room shows [doing:] and
problems; Claude login required (v0.6.0, ADR-0058); Apache-2.0 per README.
