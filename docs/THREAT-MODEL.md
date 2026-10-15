# Threat model

What a Spool hub holds, who might want it, and what stands between them. For
each boundary, this page says what it holds and what it doesn't. It
describes the posture and decides nothing: every line comes from an ADR,
SECURITY.md or the code, and an ADR changes it. A gap found while reading
this page is reported privately (SECURITY.md), never added here first.

A PR that changes what a boundary holds updates its section here in the
same PR, and updates SECURITY.md too when a gate's credential changes
(docs/QUALITY.md, review gates).

The local edition is in scope. The hosted edition (L7) gets its own model.

## Assets

| Asset | Where it lives | Protected by |
|---|---|---|
| Operator token | `<data-dir>/operator-token`, mode 0600 | The file mode; it acts as the owner and never expires (ADR-0030, ADR-0048) |
| Hub key | `<data-dir>/hub.key`, mode 0600 | The file mode (ADR-0046) |
| Claude setup-token, Telegram and Slack tokens, connection secrets and their retired values, each loop's hub MCP token | `spool.db` | Sealed with AES-256-GCM under the hub key (ADR-0046) |
| User passwords | `spool.db` | PBKDF2-SHA256, 600,000 iterations, random salt (ADR-0048) |
| Sessions, link codes | `spool.db` | Only their SHA-256 is kept (ADR-0048, ADR-0050) |
| Conversations, turns, events, the change record | `spool.db` | Redacted of every known secret on write, not encrypted (ADR-0045) |
| Files sent and received | `<data-dir>/files/`, at most 20 MB, 30 days | The data directory's mode (ADR-0037) |
| A loop's home: memory, sessions, tools, workspace, the credentials it was given | docker volume `spool-ws-<id>` | The workstation (below); not sealed |
| The host | | The workstation and egress walls, for docker loops only |
| What a release ships | GitHub releases, ghcr.io | Checksums, attestations, digests (ADR-0052) |

The data directory is 0700 and its files 0600 (`internal/datadir`).

## Actors

From the outside in:
- **A stranger on a surface:** anyone who can message a loop's bot or app.
- **An allowed sender:** a linked user's account, or a sender an admin
  allowed before ADR-0051.
- **A hub user:** a member, an admin or an owner (ADR-0048).
- **A loop.** Assume it can be steered by what it reads: a prompt
  injection in a page, an issue or a message.
- **A process inside a workstation:** a loop's tools, or what they install.
- **Anyone holding a copy of `spool.db`, the data directory or a backup.**
- **The network** between a browser and a hub that listens beyond
  loopback.
- **Whoever could alter what a release ships.**

## Boundaries

### Surface: Telegram and Slack into the hub

**What crosses:** messages, edits of them, reactions, votes and link codes,
from the platform to the router (ADR-0029, ADR-0055). Telegram is polled.
Slack connects out over Socket Mode, so no port is opened for either
(ADR-0034).

**Holds:**
- **Only an allowed sender reaches a loop.** A stranger's message gets no
  reply, creates no row, and its text is never logged. A blocked sender
  reaches nothing (ADR-0051). The hub keeps the ids of the strangers whose
  DMs it ignored for a day, in memory and at most 1000 a surface, for an
  admin to see who knocked (#744).
- **A sender who isn't allowed costs the hub little.** Their message,
  edit, reaction or vote is gated before anything it would cause, so a
  vote on a closed poll makes no call to the platform. Whether they are a
  stranger or blocked, they get at most one log line an hour, with a
  count. The count is in memory and holds 1024 senders; past that, new
  senders are counted together as `others` (ADR-0051, #788, ADR-0055).
- **A sender is allowed by linking their own account.** The control room
  mints a link code for one surface: about 49 random bits, one use,
  15 minutes, kept only as a hash. It is honoured only in a DM, and the
  account must reply YES before anything is linked. Wrong codes are limited
  per sender and, for strangers, per surface (ADR-0050, ADR-0051).
- **Only a loop's own messages are mirrored out.** Spool holds no means of
  posting as a person (ADR-0032).
- **A bot credential is validated** before it is stored (ADR-0029).

**Doesn't hold:**
- **The allowlist is hub-wide.** Any allowed sender can wake any loop and
  spend the operator's plan (ADR-0051).
- **The wrong-code limits live in memory**, so a restart resets them
  (ADR-0051).
- **Strangers can pause new linking on a surface** by spending its count
  of 50 wrong codes an hour. Allowed and linked people are unaffected, the
  other surface still links, the hub logs the pause, and an admin reads it
  in `GET /api/strangers` and can lift it (ADR-0051, #789).
- **The first group in which a loop's bot hears an allowed sender becomes
  its fleet channel** while it has none (ADR-0038). A stranger cannot bind
  one.
- **Restoring an older backup restores the links it held** (ADR-0051).
- **A Slack deletion names the message's author**, because Slack doesn't
  say who deleted it. An admin's deletion reaches the loops as the
  author's, even if the admin isn't an allowed sender (ADR-0055).

### Operator listener: the API and the control room

**What crosses:** the control room and CLI calls to `/api`.

**Holds**, in order (ADR-0030, ADR-0048, `internal/httpapi/guard.go`):
1. **Host check:** an unrecognised `Host` is refused on every `/api` path,
   which defends against DNS rebinding.
2. **Cross-site check:** a cross-site `Origin` or `Sec-Fetch-Site` is
   refused, and so is a body that isn't JSON, apart from the one upload
   route. Together with a `SameSite=Strict`, `HttpOnly` cookie, this is the
   CSRF defence.
3. **A credential:** a user's session, or the operator token. Only
   `/api/health`, `/api/version`, `/api/login` and `/api/logout` go
   without one.
4. **A one-time password still to change** confines its user to changing
   it.
5. **The role table** decides each route, and a route it doesn't list is
   an admin's (`internal/httpapi/roles.go`).

**Sessions and passwords:**
- Sessions are revocable, and expire after 14 days idle or 30 days.
  Ending a session ends the streams opened under it from their next item,
  or within a minute when the hub hears of no change: a session that runs
  out, a reset made with `spool user`, a session traded for the token.
  Asking whether a session is live is not a use of it.
- The browser keeps the session cookie for 14 days from sign-in, past a
  restart, and logout clears it. Someone else using that browser profile
  in that time is signed in as its user (#799).
- Sign-in is throttled per name tried, and an unknown name answers like a
  wrong password.
- Granting admin or owner, and resetting a password, take the acting
  owner's password (ADR-0048).

**What a member doesn't read:** other people's private conversations, a
loop's events, turns and live agent stream, and pairing codes (ADR-0048,
ADR-0050). An open stream follows a role change, a removal or a link from
its next item; a change made with `spool user` counts within a minute.

**Doesn't hold:**
- **No TLS.** Spool doesn't terminate it; beyond loopback, that is a
  proxy's job (ADR-0030).
- **The operator token is a plain file.** It keeps out other accounts, not
  processes that run as the operator.
- **The token can still be traded for a session** for one more release
  (ADR-0048).
- **A member sees names by decision:** users, loops, who is linked as
  which sender, and the change record (SECURITY.md).

### Loop listener: `/mcp` and the brokered servers

**What crosses:** a loop's MCP calls (its single `send_message` tool), and
its calls to a brokered server under `/mcp/connections/` (ADR-0026,
ADR-0043).

**Holds:**
- **Every request carries the loop's own token**, looked up by hash. The
  listener serves nothing else, and the operator listener doesn't serve
  `/mcp` (ADR-0028).
- **A loop sends only where its contract allows** (ADR-0026, ADR-0049):
  - its owner;
  - a person who has already written to it privately;
  - its channels;
  - the control room.

  Recipients are its @mentions and the author it replies to. A `reply_to`
  from another conversation is refused, and the refusal names only the
  conversation being sent to, never the other one (#719). Sends per turn
  are capped, and loop pairs have a storm guard.
- **An attachment** must be a file the loop owns, within the size cap, and
  holding no known secret.
- **The broker** checks the connection on every request, replaces the
  loop's `Authorization` with the server's secret, and never sends a secret
  over cleartext off loopback. The secret never reaches the loop
  (ADR-0043, ADR-0045).

**Doesn't hold:**
- **The token is the only gate.** Any container on the default bridge can
  reach the listener's HTTP handling before authentication (ADR-0039).
- **The broker forwards** any path, method and body under the server's
  stored URL (ADR-0045).
- **Keeping private content out of a group** is a prompt rule, not a check
  (ADR-0026).

### Cloud listener: `/mcp` for cloud loops, from the internet

**What crosses:** a cloud loop's MCP calls, from its Claude Code cloud
session over the internet, through the tunnel or TLS proxy the operator
puts in front of the listener; and the hub's own address check
(ADR-0056).

**Holds:**
- **It is off** unless the hub is started with `--cloud-listen`, and a
  port alone binds loopback.
- **Only a cloud loop's token is admitted.** A contained or bare loop's
  token is refused as an unknown one is, so a token that leaks out of a
  workstation is useless from the internet.
- **Nothing else is served.** The brokered servers, which carry
  organisation credentials, and the API are 404 there. The one route
  without a token is the address check, `GET /mcp/check/<value>`: it
  answers 204 only for a value the hub issued in the last 60 seconds,
  once, and 404 for anything else. Values are 130 bits from
  `crypto/rand`, the hub keeps only their SHA-256, and the set is capped,
  so nothing an outsider sends grows it.
- **Bounds come before authentication:** headers at 16 KiB, a body at
  1 MiB, 10 seconds for the headers and 30 for the request, two minutes
  for the answer.

**Doesn't hold:**
- **The token is the only gate**, as on the loop listener, and here any
  host on the internet can try one.
- **No rate limit.** A guess at a token or a check value is bounded by
  their size, not by a count of attempts.
- **TLS is the tunnel's or proxy's,** not the hub's: the listener speaks
  plain HTTP, and what keeps a token off the wire is the HTTPS address in
  front of it (ADR-0056 item 6).

### Workstation: the docker runtime

**What crosses:** the hub drives `docker run` and `docker exec` through
the CLI (ADR-0018).

**Holds:**
- **One container and one volume per loop.** The volume at `/home/loop` is
  the only mount, so no host path is shared and no port is published
  (ADR-0017).
- **The loop runs as user `loop`.** Credentials cross as value-less
  `--env KEY`, never in argv (ADR-0018).
- **The hub runs only images already on the host.** Every `docker run`
  refuses to pull (ADR-0052).
- **`spool-hook`, a PreToolUse hook,** refuses a recorded list of
  destructive or credential-leaking commands. Claude's settings pin hooks
  on (ADR-0042).

**Doesn't hold:**
- **Inside, the loop is root in all but name.** sudo is passwordless, and
  the hook guards against slips, not an adversary (ADR-0042).
- **Docker's default capabilities are kept**, `NET_RAW` included (ADR-0028).
- **A bare loop is uncontained.** It is a host process that can read
  everything the operator can, the data directory included (ADR-0017,
  SECURITY.md).

### Egress wall

**What crosses:** a workstation's HTTP and HTTPS, through the egress proxy
(ADR-0028).

**Holds:**
- **One way out.** Workstations sit on an internal network, and the proxy
  is their only way off it.
- **An allowlist, checked before any dial.** The proxy matches host and
  port against a built-in list (`internal/egress/allowlist.go`: Anthropic,
  GitHub, the Go, npm, PyPI and Debian archives) plus the fleet's stored
  extra hosts, which an admin edits. Entries without a port allow 80 and
  443 only, and IP literals are refused.
- **The hub is reachable** on the loop listener's port and no other.
- **Outside names don't resolve** on the internal network. Denials are
  logged by host, never by URL.

**Doesn't hold:**
- **The proxy matches names, not content.** It can't tell a fetch from an
  upload to an allowed host, a gist for example (ADR-0028, ADR-0045).
- **Sometimes there is no wall at all:** a hub started with
  `--egress-image ""`, a bare loop, and a workstation made before the wall
  until it is recreated (ADR-0028).

### Store and data at rest

**Holds:**
- **`spool.db` alone yields no credential:** every secret in it is sealed
  under `hub.key`.
- **A wrong or missing key stops the hub from starting.** It doesn't run
  on a key it can't use (ADR-0046).
- **Redaction:** known secrets, in plain and encoded forms, are redacted
  from logs, stored records and API answers (ADR-0045, `internal/redact`).
- **Backups keep the key apart:** `scripts/spool-backup.sh` writes
  `hub.key` and the operator token to a separate keys file, both files
  0600 (OPERATIONS.md).
- **A message its author deletes on Slack leaves the store**: its words,
  the words of its edits and the files kept for it go, and `deleted_at`
  stays as the record (ADR-0055).

**Doesn't hold:**
- **A deleted message's words outlive it where the loops read them:**
  their sessions and workstations, the hub's events of those turns until
  they are pruned, and any backup taken before (ADR-0055). Telegram
  reports no deletions, so a Telegram message keeps its words.
- **A copy of the whole data directory**, or a data file together with its
  keys file, yields every secret and the operator token (ADR-0046).
- **Conversations and loop volumes are not encrypted.** A volume holds the
  credentials its loop was given (OPERATIONS.md).

### Secrets a loop holds

ADR-0045 is the whole record. In short:
- **A loop holds its credentials in the clear:** its environment
  variables, the setup-token, its MCP token and its stdio MCP secrets.
  Whatever steers the loop can use them.
- **Spool keeps them** out of everything it writes and relays, and, behind
  the wall, away from hosts off the allowlist.
- **It doesn't stop** a steered loop from sending one to an allowed host
  that accepts data, or a loop with open egress from sending it anywhere.
- **The operator's levers** are listed in ADR-0045.

### What a release ships

**Holds** (ADR-0052, `.github/workflows/release.yml`):
- **A release is cut from `main` only.**
- **Its workflow is locked down:** every job names its own minimal
  permissions, and every action is pinned by commit SHA.
- **What it carries:** binaries with `SHA256SUMS`, and images by digest,
  each archive and image attested by the run that built it.
- **The binary carries its images' digests**, and `spool images` pulls by
  them. The workstation's base image is pinned by digest too.
- **A dry run installs the build** on a fresh runner before a tag is asked
  for, and the tag's run installs the published release again (#770).

**Doesn't hold:**
- **A tag ruleset is recommended, not enforced** (ADR-0052).
- **A released image freezes the Claude Code CLI** it was built with.

### Runner

The runner is in-process (`internal/loop`) and runs claude with permissions
bypassed. Containment is the runtime's job, not the runner's (ADR-0001,
ADR-0017). No threat specific to this seam is recorded. Extracting the
runner for the hosted edition will need its own model.

## Accepted risks

Findings from the 1.0 review (#777) that were accepted rather than fixed,
each with its decision and date. None yet.
