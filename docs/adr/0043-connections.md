# ADR-0043: Connections are org-level credentials and configs, defined once and attachable to loops

Date: 2026-10-03 · Status: accepted (operator decisions of 2026-10-01, recorded on #504) · Amended: 2026-10-04 (item 5: attachments, #572); 2026-10-04 (item 2: the env-var kind, #574); 2026-10-04 (item 5: per-loop secrets are connections, #576); 2026-10-05 (item 2: an mcp-server reaches its loop, #597)

## Context

A **connection** has long been in the ubiquitous language: an org-level
tool credential or config, attachable to loops. ADR-0019 made it Spool's only
GitHub responsibility, and ADR-0017 item 8 kept per-loop secret env vars
"until the L4 connections catalog". But the hub has no such object. An
operator who wants one GitHub token on three loops sets it three times, one
loop secret at a time, and an MCP server can't be given to a loop at all.

The operator opened L4 Catalog on 2026-10-01 with five epics: connections
exist in the hub (#504), they are injected (#505), the Connections page
(#506), revoke and audit (#507), and the secrets posture with scrubbing
(#30). He settled the shape of the first epic the same day. This ADR records
that shape. Slice 1 (#567) lands the object with its store, its migration
and its API. Attachments, and moving the per-loop secrets onto connections,
follow in their own slice and amend this ADR.

## Decision

1. **A connection is org-level from the start.** It belongs to the org,
   which is implicit and single in the local edition (ARCHITECTURE.md's
   *org*), not to a loop. It is defined once and can be attached to any
   loop. When orgs become explicit at L5, a connection is scoped to its
   org like everything else, and no loop ever owned one.

2. **A connection has a name, a kind, a config and a secret.**
   - **The name** is 1 to 32 of `a-z`, `0-9` and `-`, not starting with
     `-`: the alphabet a channel is named in (ADR-0038). It is unique in the
     hub and never changed.
   - **The kind** is `env-credential` or `mcp-server`.
   - **The config** is what the kind needs, and the hub checks it per kind.
     An `env-credential` names the env var its secret is carried in. An
     `mcp-server` names its transport: `http` with a URL, or `stdio` with
     a command and its arguments. A URL's `user:password@` is refused: it
     is always a credential, and the config is read back. A field another
     kind uses is refused too, so a connection reads back as it was asked
     for.
   - **The secret** is the credential itself. An `env-credential` must have
     one. An `mcp-server` may not need one. How an `mcp-server`'s secret
     reaches its server is #505's to decide.

   **Amendment (2026-10-04, #574): the env-var kind.** The operator renamed
   `env-credential` to `env-var`, labelled "env variable" in the control
   room, on 2026-10-04 (recorded on #504). Not every value a loop reads
   from an env var is a credential: a username is one that isn't. Every
   such value is still kept like a secret, which is sometimes necessary
   and never harmful. An `env-var` names its variable in `config.env` and
   must have a value, which is its secret: write-only and redacted. The
   wire value is `env-var` in the backend and the UI alike. Stored rows
   are rewritten by migration, and `env-credential` is refused as an
   unknown kind. Item 4's GitHub token is an `env-var` on `GH_TOKEN`.

   **Amendment (2026-10-05, #597): an mcp-server reaches its loop.** Each
   `mcp-server` attached to a loop is one more server in the loop's
   `--mcp-config`, beside the hub's own. `--strict-mcp-config` stays, so
   its guarantee now reads "only what Spool configured": the hub's server
   and the loop's attached ones. The servers and connectors on the
   operator's Claude account still never reach a loop. The config is
   written at every wake, as the env is, and claude reads it at every
   spawn, so an attach or detach reaches the loop the next time its claude
   starts.
   - **The name `spool` is refused** for an `mcp-server`: it is the hub's
     own server in that config. A server stored under it is left out.
   - **An `http` server's secret** is sent as `Authorization: Bearer
     <secret>`, and never in the clear: a secret on a plain `http://`
     URL is refused unless the host is loopback (`localhost`,
     `127.0.0.0/8`, `::1`), where the token never leaves the machine. A
     server stored before this amendment with such a URL and a secret is
     given no secret. This rule came from review on #598.
   - **A `stdio` server's secret** goes into the env var its `config.env`
     names, in that server's process alone. `config.env` and the secret
     come together or not at all. A `stdio` server stored before this
     amendment with a secret and no `config.env` is given no secret.
   - **The secret is written into the config file itself, never into
     claude's env.** That file is 0600 on a bare loop's host, or inside the
     docker workstation. Every Bash call a loop makes inherits claude's env,
     so a `${VAR}` reference filled from it would put the secret in the
     loop's shell. The file hands the secret to its server alone, which is
     also where `claude mcp add` keeps one. The hub's database already holds
     the secret in plain text beside that file, so keeping the value out of
     it would protect little. As with an `env-var`, a loop that goes looking
     can still read it.
   Enes decided where the secret goes on 2026-10-05.

3. **The secret is write-only.** No response carries it: the API reports
   only `has_secret`. The config is read back in full, so nothing secret
   belongs in it. The redactor learns a connection's secret the moment it
   is stored, attached or not, because a value the hub holds is one a later
   attachment can hand a loop (`internal/redact`, ADR-0017).

4. **GitHub stays an env credential until L5.** A GitHub token is an
   `env-credential` on `GH_TOKEN`. A GitHub App with its own kind waits for
   explicit orgs (operator's call, 2026-10-01).

5. **Injection doesn't change in this epic's first slice.** A connection
   reaches no loop until attachments land. Loops keep getting their
   per-loop secrets exactly as before (ADR-0017 item 8). Injecting the
   `mcp-server` kind is #505.

   **Amendment (2026-10-04, #572): attachments.** The operator attaches a
   connection to a loop and detaches it again, one loop at a time
   (`PUT`/`DELETE /api/loops/{name}/connections/{connection}`). Either is
   idempotent. A connection lists its loops by name, and a loop lists its
   connections by name and kind, never by value. A connection a loop still
   holds can't be deleted: detaching it first is the operator saying the
   loop can do without it. A deleted loop lets go of its own. Attaching
   still changes nothing a loop receives: per-loop secrets stay its env
   until #504's last slice moves them onto attached env-credentials, and
   an `mcp-server` reaches a loop with #505.

   **Amendment (2026-10-04, #576): per-loop secrets are connections.** A
   loop's env is the system's variables overlaid by every `env-var`
   attached to it, read fresh at each wake as per-loop secrets were. An
   attached `env-var` still wins a clash with a system variable. No two
   attached `env-var`s set one variable on a loop: attaching a second is
   refused. The per-loop secrets were moved, each into an `env-var` on the
   same variable with the same value, attached to its loop alone, so no
   loop's env changed. Their table is gone. The per-loop secrets routes
   stay as a shortcut onto connections:
   - Setting a variable replaces the value of the `env-var` that sets it,
     if no other loop holds that connection, or creates one and attaches it.
   - A connection other loops share is changed on the connection, never
     through one loop.
   - Removing a variable detaches it, and deletes the connection once no
     loop holds it, so a value the operator removed doesn't linger.
   Deleting a loop deletes the connections only it held, as removing a
   variable does, so a value goes with the loop it was given to; one
   another loop holds stays. Detaching on the connection's own route
   still never deletes. Enes decided this on 2026-10-04 (#576).
   An `env-var` is redacted under its variable's name, as the per-loop
   secret was. This settles the "per-loop secret env vars" ADR-0017 item 8
   kept until the catalog. The injection path is the one it described.

## Consequences

- An operator can define a credential once. Until attachments land, a
  connection only waits to be attached; its one effect is that its secret
  is redacted from everything the hub records.
- The connection table keeps the config as its kind's JSON, so a kind that
  needs another field doesn't need another column.
- The control room's Connections page (#506) builds on
  `GET`/`POST /api/connections` and `GET`/`DELETE /api/connections/{name}`.
- An attached connection's secret is as readable by its loop as a per-loop
  secret is today (ADR-0017's consequences). Scrubbing it from what a loop
  says is #30.
