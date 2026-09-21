# Security policy

Spool runs long-lived Claude Code sessions on your own machine, holds the
credentials they work with, and bridges them to a chat surface. A weakness in
it is a weakness in whatever those loops can reach, so reports are welcome and
taken seriously.

## Supported versions

Pre-1.0, only `main` is supported. Fixes land on `main` and appear in the next
tag; there are no backports and no maintained release branches. If you are
running an older tag, the first step of any fix is to update.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting: the repository's **Security** tab
→ **Report a vulnerability**. The report is visible only to the maintainer, and
the fix and advisory can be drafted in place.

Please do not open a public issue for something exploitable, and do not test
against anyone else's Spool.

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
  from, and the hub, extendable per fleet with `--egress-allow`. The list
  itself is `internal/egress/allowlist.go`, and adding to it is a PR with a
  reason (ADR-0028). A workstation reaching one of those is the design
  working.
- **Secrets you give a loop are readable by that loop**, and a prompt injection
  can therefore exfiltrate them. Injecting a secret is a decision to trust the
  loop with it ([#30](https://github.com/enes-alatas/spool/issues/30)).
- **The operator token is a file** at `<data-dir>/operator-token`, mode 0600.
  It keeps out other accounts on the machine, not processes already running as
  you.
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
- **Spool collects no telemetry** — no analytics, no crash reporting, no
  phone-home of its own. The `claude` process a loop runs is a separate
  matter: it talks to Anthropic's own hosts, `statsig.anthropic.com` among
  them, which is why they are on a workstation's default egress allowlist
  (ADR-0028). Traffic to those is Claude Code being Claude Code. If you find
  *Spool* sending anything anywhere you did not configure, that *is* a
  vulnerability.

## What the hub's trust model is

Two credentials on two listeners (ADR-0030, `docs/ARCHITECTURE.md`):

- the **operator listener** (`--listen`) serves the API and the control room.
  Every `/api` route needs the operator token except three. `/api/health` and
  `/api/version` answer before there is a token to present. `/api/logout`
  needs no credential either: refusing to end a session someone cannot prove
  they hold protects nobody. `/api/login` does need the token — it takes it
  from the request body rather than a header, because trading it for the
  session cookie is what that route is for. The same middleware refuses an
  unrecognised `Host` on every `/api` path, the open ones included — that is
  the DNS-rebinding check, and it holds before anything else is considered.
  The cross-site `Origin` / `Sec-Fetch-Site` and `application/json` checks
  apply to the routes that are not open: health and version carry nothing
  worth a cross-origin request.
- the **loop listener** (`--mcp-listen`) serves `/mcp`, and each request needs
  the requesting loop's own token. It is the one port a workstation can reach.

Neither credential is ever handed to the other's audience, and each check holds
independently of the other. A report that one of these gates can be passed
without the credential it names is exactly the kind of report this policy is
for.
