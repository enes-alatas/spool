---
title: Flags
description: "The spool binary's flags and their defaults."
weight: 1
---

| Flag | Default | What it sets |
|---|---|---|
| `--listen` | `127.0.0.1:8080` | where the control room and its API are served |
| `--data-dir` | `~/.spool` | the database, `hub.key`, loop homes and worktrees |
| `--runtime` | `auto` | docker when the daemon is reachable, else refuse to start; `bare` runs loops uncontained |
| `--trusted-host` | none | the name a hub behind a TLS proxy is reached as; [Run it as a service](../../run-a-hub/service/#tls-with-caddy) has a systemd unit and a Caddy recipe |
| `--mcp-listen` | chosen by the hub | the loops' one port; [What contains a loop](../../security/containment/) says how it is chosen |
| `--cloud-listen` | off | the port loops in Claude Code cloud sessions reach, through a tunnel or TLS proxy you put in front of it; a port alone binds loopback; an admin can switch it on in Settings → Cloud instead |

`spool --help` lists every flag. `spool token` prints the operator token again.
