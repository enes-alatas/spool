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
| The Claude login's tokens, the Claude setup-token, Telegram and Slack tokens, connection secrets and their retired values, each loop's hub MCP token | `spool.db` | Sealed with AES-256-GCM under the hub key (ADR-0046) |
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
- Granting admin or owner, demoting or removing an owner, and resetting
  a password take the acting owner's password (ADR-0048, #923).

**What a member doesn't read:** other people's private conversations
and the reactions and votes on them (#922), a loop's events, turns and
live agent stream, pairing codes (ADR-0048, ADR-0050), a connection's
config, since they read connections by name, kind and the loops that
hold them (#924), a file another user uploaded and hasn't sent, and
can't send it either, and the file of a deleted message. An open stream
follows a role change, a removal or a link from its next item; a change
made with `spool user` counts within a minute.

**Doesn't hold:**
- **No TLS.** Spool doesn't terminate it; beyond loopback, that is a
  proxy's job (ADR-0030).
- **The operator token is a plain file.** It keeps out other accounts, not
  processes that run as the operator.
- **The token can still be traded for a session** for one more release
  (ADR-0048).
- **A member sees names by decision:** users, loops, who is linked as
  which sender, and the change record (SECURITY.md).
- **A loop can repeat what it was told privately.** A member can ask a
  loop in its control-room thread about a private conversation it took
  part in, and only the prompt rule (ADR-0026) keeps it from answering.
  Accepted (below).
- **A stream whose session ended** gets no more items. It stays open on
  pings until the next item would reach it, and the hub closes it then.
  On a quiet hub, that can be a long time.

### Loop listener: `/mcp`, the brokered servers and the Claude proxy

**What crosses:** a loop's MCP calls (its single `send_message` tool),
its calls to a brokered server under `/mcp/connections/` (ADR-0026,
ADR-0043), and a contained loop's model requests under `/claude`
(ADR-0058).

**Holds:**
- **Every request carries the loop's own token**, looked up by hash. The
  listener serves nothing else, and the operator listener doesn't serve
  `/mcp` (ADR-0028).
- **Bounds:** headers at 64 KiB and 10 seconds, 30 seconds to read a
  request, and an idle connection closed after 60 seconds, so a loop
  can't hold idle connections to the hub open. There is no write
  timeout, which would cut a long call.
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
  holding no known secret in the forms the check reads (below).
- **The broker** checks the connection on every request, replaces the
  loop's `Authorization` with the server's secret, and never sends a secret
  over cleartext off loopback. The loop is never given the secret, and
  the broker never sends it back, though a server's own answer can
  (below) (ADR-0043, ADR-0045).
- **The Claude proxy keeps the hub's Claude token in the hub.** A
  contained loop logs in to it with its own hub token, and the proxy
  forwards `POST /v1/messages` and its token count alone, with the hub's
  Claude token in place of the loop's and `X-Api-Key` dropped. The
  sessions API, which the same token drives, is refused, so a contained
  loop reaches no cloud loop through it. No Claude credential is in a
  workstation's environment, its config or its volume. The loop's claude
  is told to make no call past the proxy, so it sends the hub token
  nowhere else (ADR-0058).

**Doesn't hold:**
- **The token is the only gate.** Any container on the default bridge can
  reach the listener's HTTP handling before authentication (ADR-0039).
- **The broker forwards** any path, method and body under the server's
  stored URL (ADR-0045). It relays the server's answer as sent, without
  redacting it, so a server that echoes its request's headers back hands
  the loop the secret. Accepted (below).
- **A refused `reply_to` says whether the message exists.** "No such
  message" and "not in this conversation" are different refusals, so a
  loop can learn which references exist, though never where they are.
  Accepted (below).
- **The attachment check reads a file's bytes as they are.** It doesn't
  open a compressed file or an archive, and it doesn't read UTF-16 text,
  so a known secret inside one goes out. Accepted (below).
- **Keeping private content out of a group** is a prompt rule, not a check
  (ADR-0026).
- **The proxy spends the plan for whoever holds a loop's token.** A
  steered loop can make any model request through it, as it could with
  the setup-token it used to hold, and the proxy relays the model's
  answer as sent, unredacted. What the loop can't do is copy a Claude
  credential out (ADR-0058).
- **The proxy forwards a model request's body and its other headers as
  sent.** So server-side tools (web fetch, web search, code execution,
  the MCP connector) reach hosts off the egress allowlist from
  Anthropic's side, on the hub's Claude token. The egress wall covers the
  workstation's own connections, not what a model request asks Anthropic
  to fetch. This is no wider than when the loop held the setup-token
  (ADR-0058). Accepted (below).
- **Keeping claude's calls to the proxy rests on a CLI setting.** claude
  calls `api.anthropic.com` directly with its login as the bearer unless
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` is set, and the hub sets it.
  A CLI that stopped honouring it would send a contained loop's hub token
  there, where it opens nothing. fakeclaude's FIDELITY.md records the
  behaviour as observed (ADR-0058).

### Cloud listener: `/mcp` for cloud loops, from the internet

**What crosses:** a cloud loop's MCP calls and its loop home's hook posts,
from its Claude Code cloud session over the internet, through the tunnel
or TLS proxy the operator puts in front of the listener; and the hub's
own address check (ADR-0056, ADR-0059).

**Holds:**
- **It is off** unless the hub is started with `--cloud-listen` or an
  admin switches it on in Settings → Cloud, and a port alone binds
  loopback either way. The page's default is `127.0.0.1:8090`; an admin
  who names a host can open it on any interface, as with the flag. A
  member reads whether it is on, and can't switch it. One the flag opened
  only a restart without the flag closes.
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
  `/hooks` is where a cloud session's hooks post its id and each turn
  (ADR-0059). It takes a cloud loop's token alone, as `/mcp` does.
- **The response redactor rewrites every JSON and event-stream
  response.** The hand-off, `text/plain`, is written past it on purpose
  (ADR-0059), and the route's other `text/plain` answers, its refusals
  and 404s, carry no secret.
- **Bounds come before authentication:** headers at 16 KiB, a body at
  1 MiB (16 MiB on `/hooks`, for a turn's transcript lines), 10 seconds
  for the headers and 30 for the request, two minutes for the answer.

**Doesn't hold:**
- **The token is the only gate**, as on the loop listener, and here any
  host on the internet can try one.
- **No rate limit.** A guess at a token or a check value is bounded by
  their size, not by a count of attempts.
- **TLS is the tunnel's or proxy's,** not the hub's: the listener speaks
  plain HTTP, and what keeps a token off the wire is the HTTPS address in
  front of it (ADR-0056 item 6).
  A tunnel that ends TLS off the hub's host, as Cloudflare Tunnel does at
  its edge, sees the hub token in clear, and the values of the env-var
  connections handed to each cloud session as it starts (ADR-0059).

### Cloud session: the cloud runtime

**What crosses:** the hub drives a cloud loop's Claude Code cloud session
through the `claude` CLI, on the claude.ai account's OAuth token. It starts
the session with `claude --cloud` under a pseudo-terminal, from a *loop
home* it builds for that start: a git repo of one commit, which the CLI
uploads. It sends each wake with `claude -p --cloud`. The loop home's
hooks post the session's id and each turn's transcript lines back to the
cloud listener (ADR-0059).

**Holds:**
- **The account token is sealed** under `hub.key`, known to the redactor,
  and never served back, and so is its refresh token. The sign-in runs
  the CLI's own login in a config directory of the hub's, never the
  operator's own, and the hub deletes that directory once it has read
  the tokens out. It removes any a stopped hub left as it next starts,
  before it serves, and the backup script never copies one. Only an
  admin signs in, checks the account or removes it.
- **The CLI runs hold no credential at rest.** Each run gets the access
  token in `CLAUDE_CODE_OAUTH_TOKEN` and a config dir of its own that
  holds only the onboarding and trust flags and the account's
  organization. Each runs in a directory of its own, the start in its
  loop home and every other run in an empty one, never the hub's working
  directory or the shared temp dir, whose project settings and hooks the
  CLI would load. After each run the hub removes the config dir, and logs
  by name any credential, transcript, shell snapshot or debug log the
  CLI left there. The CLI's environment is the sign-in's, with the
  credential variables stripped and its nonessential traffic off. The
  token in it is visible only to the hub's own user. The account check is
  ADR-0044's login check on the token: one haiku answer, run by the
  host's CLI, or by the workstation image's on a hub with none.
- **No setup-token leaves the machine.** A cloud session runs on the
  account, never on the setup-token.
- **The session carries one credential, a hub token of its own** (ADR-0059).
  The hub mints a fresh one for each session it starts and writes it
  into that start's loop home's `.mcp.json`, which registers the hub's
  cloud listener as the one server the home names. Stopping or killing a
  cloud loop revokes its token at once, without waiting for a turn, as
  does the next session's start. So a copy lifted out of a session dies
  with that session, and an old VM of a loop can't bind itself to a
  start of a new one: its token is refused.
- **The loop home holds the token only for its start.** The hub makes it
  in a fresh 0700 directory under a directory of its own in the temp dir,
  named for the data directory, outside the data directory, which backups
  carry. It removes the home once the session has posted its id or the
  start has failed. A hub stopped mid-start leaves its home there, and
  the next boot removes everything in that directory before any start.
  The hub uses the directory only while it is a 0700 directory of its
  own user's, checked at boot and before every run, so a temp-dir
  cleaner removing it and another user making it again gets them
  nothing, even from a hub running as root.
- **The token goes only to an HTTPS address.** The public address is
  checked at every use, whatever wrote it: one that isn't `https://`
  wakes no cloud loop and seeds no allowlist. Settings → Cloud refuses
  one too, stores it only as an origin, with no path, query or user, and
  only an admin sets it. Plain HTTP on loopback passes only on a hub
  started with `--cloud-loopback-address`, which only tests set: no cloud
  session reaches it, though the hub would check it green from itself.
- **The hub checks that the address reaches it**, when it is set, when
  the listener is switched, every five minutes, and on the page's ask:
  it requests a value it just issued through the address, follows no
  redirect, and passes only on its own listener's 204. A failure names
  its stage: the listener off, DNS, TLS, a timeout, the connection, or
  another server answering.
- **A start binds only the session its own VM names.** The hub takes a
  `SessionStart` post only while a start of that loop is pending, within
  its two minutes, once, and only with a session id of a session's shape.
  The loop can read its token and run curl, so without this it could
  rebind its own wakes into another session.
- **Deleting a cloud loop revokes its token,** with its record. The CLI
  can't archive the session, so it is left for the operator to archive
  on claude.ai.
- **A cloud loop is created only once Settings → Cloud is green:** the
  public address's last check passed, and a signed-in account's did. A
  cloud loop never moves runtime, and takes no docker settings.
- **The session link is claude.ai's or none.** The session id comes from
  a hook post, which is untrusted, so the view carries it only as a
  `https://claude.ai/code/` page.
- **What the hooks post is untrusted** and read under bounds, on the cloud
  listener's hook route: a `Stop` post over its bound ends the turn as an
  error, and so do a transcript line the hub can't read and two hours
  with no post. The lines go through the same redaction as a contained
  turn's. The hook script retries a post lost to a timeout, a refused
  connection, or a 408, 429 or 5xx, and never one the hub refused.
- **A cloud loop made under ADR-0056 starts a new session** at its next
  wake, through the CLI, on a token of its own: the hub never resumes a
  session the sessions client created.
- **Attachments don't cross.** A file sent to a cloud loop is shown as
  not kept, and the loop can't attach one: the hub never reads its own
  disk for a cloud loop.
- **A cloud session's shell found no path to another session by a
  copyable token.** A probe (ADR-0056 item 5, #866) ran as the session's
  own shell, as root. The session's `claude` reaches `api.anthropic.com`
  through a host-managed transparent egress gateway
  (`CCR_EGRESS_GATEWAY_ENABLED`, a MITM CA in the trust store, no proxy
  variable) that injects the credential. The gateway injects for nothing
  but the host-started `claude`: the real `/opt/claude-code/bin/claude -p`
  launched from the shell gets an authentication error, and a curl
  replaying its headers is 401 on every route, inference (`/v1/messages`)
  included, so these are real refusals. It isn't a namespace boundary:
  `claude` and the shell share net, user, pid and mount namespaces and
  cgroup. No replayable token is to be had: none in the environment or on
  disk (`CCR_*` token variables absent), and a scan of the session
  `claude`'s readable memory (about 400 MB, 60 regions) found no `sk-ant`,
  `oat01`, JWT, `Authorization: Bearer` or `x-api-key` value, while
  `claude` itself authenticated.

**Doesn't hold:**
- **Cloud loops are one trust domain: a cloud loop is not isolated from
  the account's other cloud loops.** The shell is root and can read
  `/proc/<pid>/mem` of the session `claude` (no Yama `ptrace_scope`,
  `Seccomp: 0`, `CAP_SYS_PTRACE` held), and that memory holds four private
  keys (`PRIVATE KEY` ×3, `RSA PRIVATE KEY` ×1) and 16 certificates
  (ADR-0056 item 5, #866). A private key is the likeliest way the gateway
  tells `claude`'s TLS connections apart from the shell's in the same
  namespace, so one may be the client identity that unlocks injection, and
  whether presenting it reaches another session was not tested — testing it
  probes Anthropic's gateway, not Spool. So the operator ships cloud loops
  as one trust domain (operator decision of 2026-10-18, #919): a cloud loop
  may act as any other cloud loop, never as a contained one — the cloud
  listener refuses a contained loop's token — and the control room says so
  when a cloud loop is created.
- **The token scan is one point in time.** A bearer resident in `claude`
  only during a request in flight, or in the two memory regions that
  didn't read, would be missed; the scan ran at one moment with `claude`
  authenticating (ADR-0056 item 5, #866).
- **The probe didn't enumerate the sandbox exhaustively.** `ss`, `iptables`
  and `nft` weren't installed, so sockets and NAT rules weren't read
  directly (the descriptor listing, which ran, showed only the
  environment-manager pipes and the log file). The in-session model refused
  every injected turn, so the checks ran from the setup-script shell, which
  shares `claude`'s namespaces and sits behind the same egress wall.
- **The check proves reach, not ownership.** A relay that forwards to
  the hub passes it, and it proves reach from this machine, not from the
  internet: an HTTPS name that resolves to an address on the hub's own
  network, such as a local proxy's, checks green too. What keeps the
  token this hub's is TLS on a hostname the operator controls (ADR-0056
  item 6).
- **The check is a request the hub makes for an admin.** Every five
  minutes, and on the page's ask, the hub GETs one fixed path on an
  HTTPS origin an admin chose, one naming the hub's own host included,
  and records how it failed. That tells an admin whether something
  answers on a port of the hub's host. Nothing the answer says is shown,
  only its status. A member sees the stage alone, not the detail, though
  the stage still says whether something answered.
- **A quick tunnel's hostname is accepted, with a warning.** A
  `*.trycloudflare.com` address is reissued once its tunnel stops, and a
  session still running sends its token to whoever holds the name then.
  The address check can hold new turns, but not a turn already in
  flight. A named tunnel on the operator's own domain, or a Tailscale
  `*.ts.net` address, which stays the tailnet's, doesn't carry this risk.
- **A cloud loop takes a turn only within a minute of a check that
  passed on the address it uses.** A failed check, no address, an
  address changed since its pass, and a hub that hasn't checked since it
  started all hold cloud loops, and their messages wait in the stored
  inbox (ADR-0056). So a hostname that changes hands gets no turn that
  starts more than a minute after the last pass. The minute is the gap:
  a turn that starts in it, like one already in flight, still sends its
  token to whoever answers the name.
- **The read-back rests on the transcript's line format,** which is the
  CLI's and not a published API. A change there fails the turn loudly
  and leaves the sends working (ADR-0059).
- **The environment pick rests on an undocumented call:** one read-only
  `GET /v1/environment_providers` on the account token, which lists the
  account's environments for Settings → Cloud. No start or send depends
  on it, so a change there breaks only the pick, and the page falls back
  to a pasted ID (ADR-0059).
- **Every session starts in the environment Settings → Cloud chose.**
  The ID's shape is checked at each start before it goes into the
  `--settings` JSON. With none chosen no session starts, rather than the
  CLI picking the account's first environment (ADR-0059). When the
  account's list is read, a choice must be on it, so a bridge to one of
  the operator's machines or an archived environment is refused. An ID
  pasted while the list fails is checked for its shape only.
- **The hub's egress allowlist doesn't bind a cloud loop.** A session
  reaches what its claude.ai environment allows, which the hub can't set
  or read. Settings → Cloud shows the list to paste there and takes the
  operator's word that they did. The confirm drops whenever the list, the
  public address or the environment changes, and until it is given again
  every cloud loop carries a warning. A list never pasted, or edited on
  claude.ai after the confirm, shows only as a fetch that fails or one
  that shouldn't have worked (ADR-0059).
- **A cloud loop's env-var connections are in its VM,** handed to its
  session as it starts (ADR-0059, #977). The hub answers the session's
  `SessionStart` post with them as `export` lines, and the hook appends
  them to `CLAUDE_ENV_FILE`. The answer comes only to the post a start of
  the loop waits for, once: a lifted token reads them only while the hub
  is starting that loop. A VM Anthropic reclaims keeps the file (run I on
  #976), so a `resume` post is never answered. Each answered hand-off is
  logged at Info, without its values. A refused post from that session
  opens the `cloud_connections` problem. The values are
  in the VM's environment file, on Anthropic's disk, as long as the
  session is kept, and outlive the loop until the operator archives it; a
  GitHub token outlives that. `mcp-server` connections never reach a cloud
  loop. It no longer starts from a repo through the Claude GitHub App: its
  GitHub reach is its `GH_TOKEN` connection's.
- **The loop can read its own token**, as a contained loop can read its
  MCP config, and use it from anywhere until its session ends. The token
  is also in the bundle the CLI uploaded, which Anthropic keeps with the
  session (ADR-0059).
- **A stop can't interrupt the turn in flight.** The CLI has no way to,
  so the turn runs on in the VM until it ends, cut off from the hub but
  with its other tools. A wake sent just before the stop may start a turn
  after it. The operator interrupts and archives the session on
  claude.ai.
- **The wake crosses in argv.** `claude --cloud` and `claude -p --cloud`
  take the envelope as an argument, so any user on the hub's host can
  read it in `/proc/<pid>/cmdline` while the run lasts, and Linux refuses
  one over 128 KiB. Whether the CLI takes it on stdin is checked on #977.
- **A copy of the data directory with its keys** yields the account
  token and its refresh token, which drive the account, not only its
  turns, and the refresh token for as long as Anthropic keeps it
  valid.
- **Sign out can't confirm the revoke.** It runs the CLI's own logout,
  which asks the source to revoke the refresh token, but the CLI reports
  success whatever the source answered (ADR-0058). Whether an accepted
  revoke also ends the access token is unknown. So a token copied before
  Sign out may work until it expires, up to eight hours, and the refresh
  token for as long as the source kept it. The operator revokes it where
  claude.ai lists the account's sign-ins.
- **A sign-in in progress holds its tokens unsealed for a moment.** The
  CLI writes them into its config directory, mode 0700 under the data
  directory, and the hub reads and deletes them as the CLI exits. A hub
  killed in that moment leaves them until it next starts, when it
  removes the directory first; the backup script never copies it.
- **On a hub with no `claude` of its own, the sign-in runs in the
  workstation image** (#952). The container runs as the hub's user and
  mounts that one directory, nothing else, so it can't read the data
  directory or a workstation. No token crosses its environment or argv:
  a refresh or logout finds the tokens in the mounted files, as the host
  CLI does. It drops every capability, gains no privilege, has a
  read-only root, and sits behind the egress wall. Its command is the
  hub's, and only the pasted code reaches its stdin. It ends itself, so
  a hub stopped mid-sign-in leaves no container past its wait.
- **A cloud turn's cost is the session's own count.** The hub prices the
  token counts the transcript reports (ADR-0059), and the loop has a
  shell where the transcript is written, so a loop can understate its
  own spend. A negative count is refused as an unreadable line, but a
  low one is believed. The figure is shown and never enforced; the
  account's usage page on claude.ai is the record.

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
- **A workstation can't forge packets.** It runs without `NET_RAW`, so it
  can't pose as another container on a network it is on (ADR-0028).

**Doesn't hold:**
- **Inside, the loop is root in all but name.** sudo is passwordless, and
  the hook guards against slips, not an adversary (ADR-0042).
- **Docker's other default capabilities are kept.** Only `NET_RAW` is
  dropped (ADR-0028).
- **A bare loop is uncontained.** It is a host process that can read
  everything the operator can, the data directory included (ADR-0017,
  SECURITY.md).

### Egress wall

**What crosses:** a workstation's HTTP and HTTPS, through the egress proxy
(ADR-0028).

**Holds:**
- **One way out.** Workstations sit on an internal network, and the proxy
  is their only way off it.
- **No workstation sees another.** Each sits on a network of its own,
  shared with the proxy alone, so none sees or reaches another
  (ADR-0028).
- **An allowlist, checked before any dial.** The proxy matches host and
  port against a built-in list (`internal/egress/allowlist.go`: Anthropic,
  GitHub, the Go, npm, PyPI and Debian archives) plus the fleet's stored
  extra hosts, which an admin edits. Entries without a port allow 80 and
  443 only, and IP literals are refused.
- **The hub is reachable** on the loop listener's port and no other.
- **Outside names don't resolve** on the internal network, on Docker
  Engine 26.0.0 or later, or a 25.x from 25.0.5, or a 23.x from 23.0.11.
  Other engines forward them to the host's resolvers, and no 24.x is
  fixed (CVE-2024-29018, #899). Denials are logged by
  host, never by URL.

**Doesn't hold:**
- **The proxy matches names, not content.** It can't tell a fetch from an
  upload to an allowed host, a gist for example (ADR-0028, ADR-0045).
- **The proxy can reach every workstation.** It sits on every loop
  network, so docker's DNS resolves each `spool-ws-<id>` name from it. An
  extra host naming a workstation would open a way from one workstation
  to another. The built-in list names none, so only an admin's entry can
  (ADR-0028).
- **Every workstation can reach the OAuth token endpoint,**
  `platform.claude.com`, which the built-in Claude group allows for the
  hub's own sign-in (operator decision of 2026-10-19, #952). So can every
  cloud environment, which takes the built-in list too. It is
  harmless only while no loop holds a refresh token: the hub keeps it,
  and a contained loop reaches the model through the Claude proxy
  (ADR-0058).
- **Sometimes there is no wall at all:** a hub started with
  `--egress-image ""`, a bare loop, and a workstation made before the wall
  until its next wake replaces it, or for as long as its image is gone
  (ADR-0028).

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
  That includes the Claude login, which renews itself until someone
  signs out or revokes it at claude.ai, where a setup-token had a fixed
  life (ADR-0058).
- **A setup-token a sign-in replaces stays valid at Anthropic.** The hub
  clears it and stops redacting it, but can't revoke it: it lives out
  its year unless the operator revokes it at claude.ai. A copy of it
  made before the sign-in, in a backup or a loop's old output, still
  works (ADR-0058).
- **Conversations and loop volumes are not encrypted.** A volume holds the
  credentials its loop was given (OPERATIONS.md).

### Secrets a loop holds

ADR-0045 is the whole record. In short:
- **A loop holds its credentials in the clear:** its environment
  variables, its MCP token and its stdio MCP secrets. A contained loop
  holds no Claude credential: its MCP token is also its login to the
  Claude proxy (ADR-0058).
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

Findings from the 1.0 review (#777) and since that were accepted rather
than fixed, each with its decision and date (#917). Each one's line
above says what it is; this is why it stays.

- **A loop can repeat what it was told privately** (Operator listener).
  Accepted 2026-10-17: keeping a private conversation private is
  ADR-0026's prompt rule by design, and who is made a member is the
  operator's lever.
- **The broker relays a server's answer unredacted** (Loop listener).
  Accepted 2026-10-17: a loop the Claude Code CLI connects to a server
  directly holds the server's secret outright, so the broker is already
  better than that baseline; an echo needs a server that misbehaves, and
  the operator chooses the servers.
- **A refused `reply_to` says whether the message exists** (Loop
  listener). Accepted 2026-10-17: references are sequential, and the
  refusal names no conversation and no words.
- **The attachment check doesn't open archives, compressed files or
  UTF-16 text** (Loop listener). Accepted 2026-10-16 (#912, ADR-0045):
  opening them costs CPU on the send path and a decompression-bomb
  surface, against a disguise ADR-0045 already leaves to deliberate
  obfuscation.
- **Server-side tools reach hosts off the allowlist** (Loop listener).
  Accepted 2026-10-18 (#932): they run on Anthropic's side, not the
  hub's host, and are no wider than when a loop held the setup-token.

Three more findings were decided as fixes, and all three are fixed:
reactions and votes on private messages (#922), members reading
connection configs (#924), and demoting or removing an owner (#923).
Their lines are gone.
