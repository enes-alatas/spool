# Security policy

Spool runs long-lived Claude Code sessions on your own machine, holds the
credentials they work with, and bridges them to a chat surface. A weakness in
it is a weakness in whatever those loops can reach, so reports are welcome and
taken seriously.

## Supported versions

Pre-1.0, only `main` is supported. Fixes land on `main` and appear in the next
tag; there are no backports and no maintained release branches. If you are
running an older tag, the first step of any fix is to update.

From 1.0, only the latest minor release gets security fixes. An older
minor is fixed by upgrading to the latest, with `spool upgrade`
([OPERATIONS.md, "Upgrade"](docs/OPERATIONS.md#upgrade)); there are still
no backports.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting: the repository's **Security** tab
→ **Report a vulnerability**. The report is visible only to the maintainer, and
the fix and advisory can be drafted in place.

Please do not open a public issue for something exploitable, and do not test
against anyone else's Spool.

For a problem that is not a vulnerability, use the **Report a problem** issue
form instead ([CONTRIBUTING.md](CONTRIBUTING.md)).

**Never put a real credential in a report.** If a token or key is part of the
finding, quote at most eight characters of it and then an ellipsis —
`ghp_abcd…`. A structural prefix counts toward the eight, unless the prefix is
itself longer than eight, in which case count them after it
(`sk-ant-api03-Xf9k2b…`): enough for us to match the value against its store,
never enough to use it. Say where it lives instead of what it is.

Spool has one maintainer and no on-call, so this policy makes no promise about
response times — reports are read and answered as soon as they can be, and
saying more than that would be inventing a commitment. What follows from it is
practical: if a report needs a credential rotated on your side, rotate it
immediately rather than waiting for a reply.

Coordinated disclosure, please: give a fix a reasonable window before going
public. There is no bug bounty. Reporters are credited in the advisory unless
they ask not to be.

## Already known, and not a vulnerability

These are documented properties of the design. A report that one of them is
true will be closed with a pointer here — which is not a judgement on whether
they *should* be true, only that they are known and recorded.

- **A bare loop is uncontained.** With the `bare` runtime a loop is a
  subprocess under your own user account, with your network and your files. It
  can read `spool.db`, the operator token, and anything else you can. That is
  what the *uncontained* badge means (ADR-0017); the docker runtime is the
  default whenever a daemon is reachable, and a docker workstation has neither
  the data directory nor any route off its network but the egress allowlist —
  Anthropic's hosts, GitHub, the package registries a workstation installs
  from, and the hub, extendable per fleet through a stored list of extra
  hosts, which `--egress-allow` seeds on the first start and
  `/api/settings/egress` edits while the hub runs. The built-in list itself is
  `internal/egress/allowlist.go`, and adding to it is a PR with a reason
  (ADR-0028). A workstation reaching one of those is the design
  working.
- **Secrets you give a loop are readable by that loop**, and a prompt injection
  can therefore exfiltrate them. Injecting a secret is a decision to trust the
  loop with it. Spool keeps a known value, plain or in its usual encodings,
  out of everything it records and sends, out of a teammate's input, and, for
  a docker loop behind the egress wall, away from hosts off the allowlist. A
  bare loop, or any loop on a hub started with `--egress-image ""`, reaches
  every host. Even behind the wall, Spool does not stop a steered loop from
  sending a credential it holds to an allowlisted host that accepts data, such
  as a GitHub gist. [ADR-0045](docs/adr/0045-what-spool-defends-for-secrets.md)
  lists what is and isn't defended, and the levers you hold.
- **The operator token is a file** at `<data-dir>/operator-token`, mode 0600.
  It keeps out other accounts on the machine, not processes already running as
  you.
- **The hub key is a file too.** Every secret in `spool.db` is sealed under
  `<data-dir>/hub.key`, mode 0600, so a copy of the database alone holds no
  credential. A copy of the whole data directory holds the key as well, and
  anything running as you can read both. Losing the key loses the secrets:
  the hub won't start until you restore it, or start it once with
  `--forget-secrets` and enter them again (ADR-0046).
- **Spool does not terminate TLS.** A hub on a non-loopback `--listen` is a
  supported posture and the operator token is what protects it, but
  confidentiality across an untrusted network is a terminating proxy's job
  (ADR-0030). Such a hub is reached as the proxy's name, which must be given to
  `--trusted-host` — without it the Host and Origin checks refuse every request
  that posture produces, and a 403 there is the guard working, not a bug.
- **A loop acts with the authority you gave it.** Spool does not attempt to
  keep a loop from misusing its own GitHub token, its own chat surface, or its
  own workspace. Containment is about the blast radius of the machine, not
  about supervising an agent's judgement.
- **A member sees names.** The names of users and loops, which sender is
  which user, and who changed what on `/api/changes` are visible to a
  member, by decision: a name is not a secret (ADR-0048). What a member is
  kept from is listed under the trust model below. A member reading one of
  *those* is a vulnerability.
- **Spool collects no telemetry** — no analytics, no crash reporting, no
  phone-home of its own. The `claude` process a loop runs is a separate
  matter: it talks to Anthropic's own hosts, `statsig.anthropic.com` among
  them, which is why they are on a workstation's default egress allowlist
  (ADR-0028). Traffic to those is Claude Code being Claude Code. If you find
  *Spool* sending anything anywhere you did not configure, that *is* a
  vulnerability.

## What the hub's trust model is

Three listeners and two credentials (ADR-0030, ADR-0056,
`docs/ARCHITECTURE.md`): the operator's, and each loop's own, which the cloud
listener takes too. The third listener is off unless set. Neither credential
is ever handed to the other's audience, and each check holds independently of
the other. For every boundary, what crosses it, what it holds and what it
doesn't, see
[`docs/THREAT-MODEL.md`](docs/THREAT-MODEL.md).

**The operator listener** (`--listen`) serves the API and the control room.

- **Who gets in.** Every `/api` route needs a credential, with three
  exceptions:
  - `/api/health` and `/api/version` answer before a caller can have one.
  - `/api/logout` asks for nothing: refusing to end a session someone
    cannot prove they hold protects nobody.
  - `/api/login` takes the credential it trades for a session in its body.

  The credential is one of two:
  - **A user's session**, from signing in with a username and password
    (ADR-0048). The cookie holds only a random session ID, and the store
    keeps only that ID's SHA-256, so a copy of `spool.db` holds no live
    session. Sign-out, a password change, a reset and a removal end
    sessions. So do 14 days without use, or 30 days after sign-in.
  - **The operator token**, as a bearer header, which acts as the owner. It
    can still be traded for a session at `/api/login` for one more release.
- **Guessing passwords.** Sign-in is throttled per username tried, whether
  or not a user has it: after 5 failures the name locks, and the lock
  doubles up to 15 minutes. An unknown name gets the same answer and takes
  the same time as a wrong password.
- **What each role may call.** A user with a one-time password still to
  change reaches only `/api/me`, `/api/me/password` and the open routes.
  Past that, the role decides (ADR-0048):
  - a **member** reads the fleet and talks to its loops;
  - an **admin** also creates, changes and operates loops, surfaces and
    senders;
  - an **owner** also manages the hub's users.

  A route nobody has classified is an admin's, so a new route is closed to
  members until someone decides otherwise.
- **What a member does not read** (ADR-0048, ADR-0050): another person's
  private conversations and the reactions and votes on them, a loop's
  events, turns and live agent stream, which can carry any private message,
  pairing codes, and a connection's config, since they read connections
  by name, kind and the loops that hold them. A member does read their own
  private conversations: those of the people they are linked as, and what
  a loop they own said with them on `owner_dm`. An open stream follows a
  change of role, a removal or a link from its next item: a demoted admin
  reads no more, and a removed user's stream ends, as does a stream whose
  session was signed out of, expired, or ended by a password change or a
  reset. A change made with `spool user`, which the running hub does not
  hear of, counts within a minute, as does signing out of a session traded
  for the operator token. Asking whether a session is still live does not
  count as using it, so an open stream keeps no idle session alive.
- **Before anything else** is considered, the same middleware refuses an
  unrecognised `Host` on every `/api` path, the open ones included. That is
  the DNS-rebinding check. The cross-site `Origin` / `Sec-Fetch-Site` and
  `application/json` checks apply to every route that isn't open.

**The loop listener** (`--mcp-listen`) serves `/mcp` and each loop's brokered
MCP servers under `/mcp/connections/`. Every request needs the requesting
loop's own token. It is the one port of the machine a workstation can reach.

**The cloud listener** (`--cloud-listen`, off unless set) serves `/mcp` to
cloud loops, which reach it over the internet through the operator's tunnel
or TLS proxy (ADR-0056). It admits a cloud loop's own token and refuses
every other loop's. Its one route without a token, `GET /mcp/check/<value>`,
answers only for a value the hub issued in the last minute, once. Request
size and time are bounded before the token is read.

**Who a loop answers on a surface** (ADR-0051). A Telegram or Slack sender
reaches a loop only once they are *allowed*. A user becomes an allowed sender
by linking their own account:
- the control room gives them a one-surface link code, `link-` and ten random
  characters. It works once, expires after 15 minutes, and the store keeps
  only its SHA-256;
- they send it in a DM to any loop's bot or app;
- the hub asks that account to confirm, and links it only when it replies
  YES.

Nobody links someone else's account, an admin included. A blocked sender
reaches nothing, a code included. A stranger's message gets no reply, is never
stored, and has its text never logged.

**Secrets at rest.** Every credential the store keeps (connection secrets, bot
tokens, the loops' MCP tokens, the setup-token) is sealed in `spool.db` under
`<data-dir>/hub.key` (ADR-0046). Passwords are kept only as salted
PBKDF2-SHA256 hashes (ADR-0048).

A report that any gate above can be passed without the credential it names,
or that a role reads or does what its line says it doesn't, is exactly the
kind of report this policy is for.

## Verifying what you run

A release carries its binaries for Linux and macOS, `SHA256SUMS`, and the
digests of the workstation and egress images (ADR-0052). Each archive and each
image has a GitHub attestation of the workflow run that built it.
[OPERATIONS.md](docs/OPERATIONS.md#upgrading) has the commands:
- `sha256sum -c` against `SHA256SUMS` catches a broken download;
- `gh attestation verify` catches an archive and checksums swapped together;
- `./bin/spool images` prints each image as a pull by its digest, which is
  that image's own check.

The hub never pulls an image itself: every `docker run` it issues refuses to
pull, so it runs only what you put on the machine. A release whose files don't
verify, or a published image whose digest differs from the one in its
release's notes, is a vulnerability report.
