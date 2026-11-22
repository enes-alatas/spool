---
title: Quick start
description: "Build Spool from source, sign in and talk to your first loop."
weight: 1
---

You need Linux, macOS or Windows with [WSL2](https://learn.microsoft.com/windows/wsl/install), with [Docker](https://docs.docker.com/get-docker/) running, `git`, [Go](https://go.dev/dl/) 1.25+, [Node](https://nodejs.org/) 20.19+ or 22.12+, and [Claude Code](https://claude.com/claude-code) on a Claude subscription, installed on the machine Spool runs on: the hub signs in with it in step 3.

1. **Get Spool and build it.**

   ```bash
   git clone https://github.com/enes-alatas/spool && cd spool
   make build image
   ```

   That builds `bin/spool`, the control room inside it, and the two Docker images a loop runs in.

2. **Start it and sign in.** Run `./bin/spool`. On its first start it prints a one-time password for the user `admin`. Open http://127.0.0.1:8080, sign in as `admin` with that password, and choose your own. Missed the password? `./bin/spool user reset admin` prints a new one.

   The control room opens on **Set up your fleet**: three cards, in the order below. Each checks itself and turns done on its own.

   {{< clip name="setup" width="1440" height="990" label="Set up your fleet, its three cards done in turn: Sign in with Claude ticks the Harness card; New loop creates a loop, which takes its first turn; Attach a bot takes a Telegram token and gives a link code, and the Chat surface card ticks." >}}

3. **Harness.** Choose **Sign in with Claude** on the card. claude.ai opens in a new tab and asks you to allow the sign-in; paste the code it shows back into the card. Contained and cloud loops run on your Claude plan through that login. Each Claude login is the subscription of the person who signs it in. Spool never pools or resells capacity, and you follow your plan's terms. A hub can hold more than one: see [Claude logins](../../guides/claude-logins/).

4. **Loops.** Choose **New loop**, and give it a name and a mission. Its first wake builds its workstation, which takes a minute or two the first time; the card is done when that wake's first turn ends.

5. **Chat surface.** Choose **Attach a bot**, then **Attach Telegram** or **Attach Slack**.
   - **Telegram:** send **@BotFather** `/newbot` in Telegram, and paste the token it gives you.
   - **Slack:** follow the dialog's **Create app from manifest**, then paste the app's two tokens. [Slack](../../start/chat-surfaces/slack/) has the steps.

   Then choose **Get a link code**, send the code to your bot in a direct message, and reply **YES** when it asks. That links your account. On Telegram it also makes you the owner of every loop that has none; a Slack link makes nobody an owner, so choose yourself as the loop's **owner** on its page ([Slack](../../start/chat-surfaces/slack/) step 6). Message the bot once more: the card is done when the loop answers you there.

   No chat app? You can talk to a loop on its page in the control room instead. Once the Harness and Loops cards are done, choose **Skip for now** on the Chat surface card to finish the setup page without one, and attach a bot later on the loop's page.

## Other ways to run it

- **From a release,** with no Go, Node or `make`: [From a release](../from-a-release/).
- **On a server, as a service,** with TLS and backups: [Run a hub](../../run-a-hub/).
- **Without Docker.** `./bin/spool --runtime bare` runs each loop as a plain process under your own account and your own `claude` login, *uncontained*. It needs no `make image` and no step 3. Read [What contains a loop](../../security/containment/) before you choose that.
