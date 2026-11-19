---
title: Langfuse beside the hub
description: "An optional Langfuse beside the hub, to see each loop's spend, tokens and failures without its content."
weight: 7
---

Langfuse is optional. With it, an operator sees each loop's spend, tokens,
turn times, tool mix and failures, but no content. It runs as
the `langfuse` compose profile in `docker/langfuse/compose.yml`. It's off
by default, and the hub runs the same without it. The commands run from
the checkout as a user who can reach docker, on a hub that uses the docker
runtime.

- **Footprint:** about 2.6 GiB of RAM idle, whatever the traffic (1.8 GiB
  measured just after a start), and about 40 KB of disk a turn. That's six
  containers: web, worker, ClickHouse, Postgres, Redis and MinIO.
- **What it reaches:**
  - The datastores and the worker sit on an internal network with no
    route out.
  - The web container also joins the hub's egress network. There the
    egress proxy's relay reaches it as `http://langfuse-web`. It never
    joins a loop's network.
  - It also joins a bridge network that carries only the UI's port, on
    `127.0.0.1:3000`.
  - Langfuse's own telemetry is off, sign-up is off, and no model
    provider is set.

## Turning it on

The profile joins the hub's egress network, which the hub makes at its
first start with the docker runtime. Start the hub first, then:

```bash
bash scripts/langfuse-setup.sh --admin-email you@example.com
docker compose -f docker/langfuse/compose.yml \
  --env-file docker/langfuse/langfuse.env --profile langfuse up -d --wait
```

- **The setup script:** it writes two files, both 0600, ignored by git,
  and never overwritten:
  - `docker/langfuse/langfuse.env`, which compose reads. Every secret in
    it is generated: the datastores' passwords, the project's key pair,
    and the admin's password. Keep it as you'd keep `hub.key`, and keep
    it from the hub's user: every bare loop runs as that user.
  - `docker/langfuse/hub.env`, the key pair alone, which the hub reads.
    Anyone with the key pair can read every trace.
- **A hub with a non-default proxy image:** its egress network has the
  image's name. Pass it with `SPOOL_EGRESS_NETWORK=<name>` to the setup
  script, which writes it into the env file.
- **Signing in:** the UI is at `http://127.0.0.1:3000` on the hub's
  machine, through `ssh -L 3000:127.0.0.1:3000` from elsewhere. Sign in as
  the admin with the password from the env file.
- **Through Caddy:** add a site with `reverse_proxy 127.0.0.1:3000`, then
  add `LANGFUSE_URL=https://<that name>` to both files, run the `up`
  again, and restart the hub. The UI keeps its own login.
- **Telling the hub:** give it `hub.env` and restart it.
  - **Give the hub's user the file**, if you ran the setup script as
    someone else:
    `sudo chown spool docker/langfuse/hub.env`, keeping it 0600. A file
    the hub can't read doesn't stop it. It starts with whatever it had
    stored and warns `the hub's Langfuse file is not read`.
  - **At start the hub reads** `docker/langfuse/hub.env` in the
    checkout its binary sits in (`/opt/spool/bin/spool` reads
    `/opt/spool/docker/langfuse/hub.env`), or the file `--langfuse-env`
    names. Traces are then on, with the file's key pair and its
    `LANGFUSE_URL` if you set one. The log says
    `langfuse settings from the hub's Langfuse file`.
  - **A file the hub can't use stops it.** That covers a line that isn't
    `NAME=VALUE`, a missing `LANGFUSE_PUBLIC_KEY` or
    `LANGFUSE_SECRET_KEY`, or a `LANGFUSE_URL` that isn't https, or http
    on this machine. The error names the file and what is wrong in it.
  - **While the file is there, it wins.** `PUT /api/settings/langfuse`
    refuses to change what it set. To change a setting, edit the file and
    restart the hub.
  - **A Langfuse set up before `hub.env` existed** has `langfuse.env`
    alone. Write the hub's file from it, then give it to the hub's user:
    ```bash
    (umask 077; grep -E '^LANGFUSE_(PUBLIC_KEY|SECRET_KEY|URL)=' \
      docker/langfuse/langfuse.env > docker/langfuse/hub.env)
    chmod 600 docker/langfuse/hub.env   # > keeps an existing file's mode
    ```

## Reading figures back

- **The hub reads each loop's figures back** from the read address,
  with the read key, every five minutes. With the profile,
  `hub.env` sets both. `GET /api/loops/{name}/figures?window=24h` serves
  a loop's turns, failures, spend, tokens, turn durations, tool mix and
  per-turn rows over `1h`, `24h`, `7d` or `30d`, numbers and names only.
- **Spend is the hub's own.** Langfuse holds no cost for Claude Code's
  spans, so the figures take spend from the turns the hub prices
  already, joined to a trace by its session. Tokens, durations, tools and
  failures come from Langfuse.
- **`langfuse` in the answer says what it holds:** `ok`; `stale`, the
  last figures while Langfuse isn't answering; `pending`, before the
  first read; `off`, no read address or read key; `unsupported`, a cloud
  loop, which exports nothing.
- **A restart reads the last 30 days again.** The hub keeps the figures
  in memory, not in `spool.db`.
- **A bare loop's figures are trusted as far as the loop**: it can post
  spans in another loop's name.

## Turning it off

- **Moving `hub.env` away and restarting the hub** turns every loop's
  export off at its next session start. The hub clears the settings it
  took from the file and warns `the hub's Langfuse file is gone`,
  naming it. If you didn't mean to turn traces off, that line is the
  clue: the file moved, the binary runs from another checkout, or
  `--langfuse-env` is mistyped. Settings stored through the API, with no file,
  are turned off by clearing the upstream there.
- **With bare loops,** also rotate or delete the project's key pair in
  Langfuse's UI. A bare loop holds the key in its environment and may
  have kept a copy.
- **Stopping Langfuse:**
  `docker compose -f docker/langfuse/compose.yml --env-file docker/langfuse/langfuse.env --profile langfuse down`
  stops it and keeps its data.
- **Removing it for good:** add `-v` to that command to delete its
  volumes, then delete `docker/langfuse/langfuse.env` and `hub.env` and
  restart the hub, which then turns export off. The hub's egress network
  is the hub's, so it stays.

## Backups and upgrades

- **Not backed up:** `spool-backup.sh` doesn't back up Langfuse's volumes.
  They hold telemetry, which builds up again once loops run, so losing
  them loses history, not the fleet.
- **Upgrading Langfuse:** the images are pinned by digest in the compose
  file, so it moves only with a Spool release that changes them. After
  pulling one, run the `up` again.
