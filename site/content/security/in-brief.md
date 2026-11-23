---
title: Security in brief
description: "The short version: how each loop is contained and what the hub holds."
weight: 2
---

{{< diagram name="containment-brief" alt="Each loop runs in its own container on an internal network. Its only way out is the egress proxy, which reaches the hosts on your allowlist and one hub port that serves only what loops use. The control room and its API are not reachable from a loop. A bare loop has no container: it runs under your account, with your files and your network." >}}

- **Contained by default.** Each loop runs in its own container with an egress allowlist (**Settings → Egress**) and no route to your machine but one hub port, which serves only what loops use and never the control room or its API.
- **Uncontained only when you ask.** Loops run with permission prompts bypassed. Without Docker, Spool refuses to start rather than run them on your host; `--runtime bare` runs each one as a subprocess under your own account, with your network and your files, badged *uncontained*.
- **Signed in.** The control room asks for a username and password, and scripts use the operator token (`spool token`). Binding to localhost is not a boundary on its own.
- **Secrets sealed.** Claude logins, bot tokens and connection secrets, and the setup-token if you still use one, are sealed under `hub.key` in the data directory. Back it up apart from `spool.db`.

[What contains a loop](../containment/) has the whole picture, including what containment does not do. [SECURITY.md](https://github.com/enes-alatas/spool/blob/main/SECURITY.md) says how to report a vulnerability, and [THREAT-MODEL.md](https://github.com/enes-alatas/spool/blob/main/docs/THREAT-MODEL.md) says what each boundary holds and does not.
