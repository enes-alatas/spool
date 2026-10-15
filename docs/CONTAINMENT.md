# What contains a loop

What keeps a loop from your machine, what it does not stop, and how the hub keeps its own secrets. The README has the short version.

Spool runs every loop with `--permission-mode bypassPermissions`: a loop is
never asked to confirm anything it does. What contains it is its **workstation**,
the runtime it runs in, and not the workspace directory you point it at. A
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
chooses it for you: a daemon being down is not consent.

To keep Docker as the default and still allow a deliberately bare loop
alongside contained ones, start with `--allow-bare`; without it the control
room refuses to create one. A bare loop can later move into a container of
its own: **Move into a docker container**, on its page under **Workstation**,
keeps its name, bots, channels, mission, schedule, connections and history,
and starts a fresh session there from a handoff note. There is no moving it
back.

Two things containment does not do. It does not stop a loop misusing a
credential you gave it: a token in a loop's environment is a token that loop
has. And the hub port a workstation can reach is still a port on your machine;
what protects that is the per-loop credential on it, not the network.

A loop reaches Spool's own MCP server, which carries the one tool it needs to send messages, plus any MCP server you attach to it as a connection. MCP servers and connectors on your Claude account are never passed to a loop (`--strict-mcp-config`).

Spool serves two listeners, and a third when you ask for it. `--listen` is yours: the control room and its API. `--mcp-listen` belongs to the loops: it is the one port a containerized workstation is allowed to reach, and it serves the MCP endpoint and nothing else. Keep them apart. A workstation that could reach the API port could read every conversation and create an uncontained loop. `--cloud-listen`, off unless set, is for loops in Claude Code cloud sessions, which reach it over the internet through your tunnel or TLS proxy (ADR-0056).

Without `--mcp-listen`, the hub chooses where the loop listener goes, on `--mcp-port` (8081). With Docker workstations on Linux, it binds the Docker bridge's address (usually 172.17.0.1). Your workstations can reach that address. Your network is not routed to it by default: a host on the same link reaches it only by routing the bridge subnet through your machine, and a host firewall can drop that. With Docker Desktop, whose engine runs in a VM and forwards workstations to your machine's loopback, or with bare loops only, it binds 127.0.0.1. The hub logs which it chose and why. An explicit `--mcp-listen` always wins. `--listen` stays on localhost.

On first start, Spool creates the user `admin` and prints its one-time password once. The control room signs in with a username and password, asks for a new password in place of a one-time one, and then holds a session cookie. `spool user` adds, resets, lists and removes users. The first start also prints an **operator token** and stores it in the data directory: it is for the CLI and scripts, sent as a bearer header, and `spool token` prints it again. Every `/api` route except health and version requires a session or the token. Binding to localhost is not a boundary, because any other local process, and any page in your browser, can reach that port too.

Spool also mints a **hub key**, `hub.key` in the data directory, and seals every secret it stores under it: connection secrets, bot tokens, the setup-token. A copy of `spool.db` alone holds none of them. Back up `hub.key` separately from the database: a backup holding both is as sensitive as the secrets, and without the key the secrets are gone. If the key is lost, the secrets can't be recovered without it, and the hub won't start until you restore it, or start it once with `--forget-secrets`, which revokes the connections, unbinds the bots and clears the setup-token so you can enter them again. [OPERATIONS.md](OPERATIONS.md#back-up-and-restore) has a backup and restore that keeps the two apart and covers the workstation volumes too.
