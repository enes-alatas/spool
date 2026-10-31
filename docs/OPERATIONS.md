# Operating a hub

*How to keep a hub running once it is up: run it as a service behind TLS,
back it up, restore it, and upgrade it. The README gets a hub started; this
page is for after that.*

## Run it as a service

These steps take a fresh Debian or Ubuntu server to a hub that starts at
boot, restarts on failure, and serves the control room over HTTPS at a name
you own. The rest of this page assumes this layout:

| What | Where |
|---|---|
| The checkout and `bin/spool` | `/opt/spool`, built by you (README steps 1–3) |
| The hub's user | `spool`, a system user in the `docker` group |
| The data directory | `/home/spool/.spool` |
| The control room | `127.0.0.1:8080`, reached only through the proxy |
| The proxy | Caddy on 443, with a certificate for `spool.example.com` |

Use Docker Engine 26.0.0 or later. Of the older lines, only a 25.x from
25.0.5 and a 23.x from 23.0.11 are fixed; no 24.x is. Any other engine lets a workstation resolve outside
names, which can carry data past the egress allowlist (CVE-2024-29018),
and the hub warns at boot when it finds one.
`docker version --format '{{.Server.Version}}'` prints yours.

Each docker loop gets a network of its own (ADR-0028), and Docker's default
address pools hold about 30 networks on one engine, other containers'
included. A loop past that fails to wake with an error naming
`default-address-pools`. To run more, widen the pools in
`/etc/docker/daemon.json` and restart Docker:

```json
{ "default-address-pools": [{ "base": "10.201.0.0/16", "size": 24 }] }
```

Pick a base no network around your machine uses. A /16 split into /24s holds
256 networks.

Spool does not terminate TLS (ADR-0030). The proxy does that, and the hub
stays on loopback behind it. Who may call the API does not change: a session
or the operator token, as on localhost.

### The hub's user

```bash
sudo useradd --system --create-home --home-dir /home/spool --shell /usr/sbin/nologin spool
sudo usermod -aG docker spool
```

Membership of `docker` is what lets the hub run workstations. It is also
root on this host to anyone who can act as `spool`, which is why nothing
but the hub runs as that user.

### The unit

`/etc/systemd/system/spool.service`:

```ini
[Unit]
Description=Spool hub
Wants=network-online.target docker.service
After=network-online.target docker.service

[Service]
User=spool
Group=spool
SupplementaryGroups=docker
WorkingDirectory=/home/spool
ExecStart=/opt/spool/bin/spool --data-dir /home/spool/.spool \
    --listen 127.0.0.1:8080 --trusted-host spool.example.com
Restart=on-failure
RestartSec=5
# The hub drains HTTP for up to 10s, then stops each loop's claude.
TimeoutStopSec=30
# On `spool serve`, stdout carries only the operator token and the admin's
# one-time password, printed once at the first start. The log is on stderr.
# Under systemd, stdout is the journal, which members of `adm` and
# `systemd-journal` can read, so it goes nowhere instead. stderr follows
# stdout unless it's named, so it's named: the log stays in the journal.
StandardOutput=null
StandardError=journal
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full

[Install]
WantedBy=multi-user.target
```

- `--trusted-host` names the proxy's host. Without it, the hub's Host and
  Origin checks refuse every request the proxy forwards, with
  `unexpected Host` and a 403. That is the guard working, not a bug.
- `Restart=on-failure` covers a hub that cannot bind or open its database:
  each exits non-zero (#253), so systemd sees the failure and retries.
- The hardening lines take nothing the hub uses. It writes only under its
  data directory and the loops' workspaces, and it shares no host path with
  a container: workstations get named volumes, and the egress proxy's
  allowlist is streamed in.

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now spool
journalctl -u spool -n 50        # `runtime ready`, and where the loop listener bound
```

The first start mints the operator token and the `admin` user's one-time
password. It prints them to stdout, which this unit discards, so neither
reaches the journal. Print each at your own terminal instead:

```bash
sudo -u spool /opt/spool/bin/spool token --data-dir /home/spool/.spool              # the operator token
sudo -u spool /opt/spool/bin/spool user reset admin --data-dir /home/spool/.spool  # a fresh one-time password
```

Sign in as `admin` with that password and set your own. The operator
token is a full API credential that doesn't expire, so keep it with the
keys file (see Back up and restore), not in shell history or a ticket.

### TLS with Caddy

Point `spool.example.com`'s DNS at the server, install Caddy from its
[Debian/Ubuntu packages](https://caddyserver.com/docs/install#debian-ubuntu-raspbian),
and replace `/etc/caddy/Caddyfile` with:

```caddyfile
spool.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

Then run `sudo systemctl reload caddy`. Caddy obtains and renews the
certificate on its own, redirects http to https, and passes everything the
hub needs:
- It keeps the `Host` the browser sent, which `--trusted-host` matches.
- It sets `X-Forwarded-Proto: https`, so the hub marks its session cookie
  `Secure`.
- It flushes the control room's event streams as they arrive.

**nginx instead:** the same three things have to be said explicitly.

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_buffering off;          # the event streams
    proxy_read_timeout 1h;        # a quiet stream is not a dead one
}
```

### The firewall

Open 80 and 443 for Caddy, and SSH. Open nothing for the hub.

```bash
sudo ufw allow OpenSSH && sudo ufw allow 80,443/tcp && sudo ufw enable
```

The control room port is on loopback, so the network cannot reach it at all.
The loop listener (`--mcp-listen`) binds the docker bridge
([CONTAINMENT.md](CONTAINMENT.md)), which the network is not routed to. A default-deny inbound
policy can still drop the workstations' own traffic to it, since that
traffic arrives from the bridge. The symptom is a docker loop that never
answers while its wake times out in the hub log. The fix is to allow that one
port from the bridge only:

```bash
sudo ufw allow in on docker0 to any port 8081 proto tcp
```

The cloud listener (`--cloud-listen`, for loops in Claude Code cloud sessions,
ADR-0056) is off unless you set it, or an admin switches it on in
Settings → Cloud. The page opens it on `127.0.0.1:8090` unless told
otherwise, and the hub opens it there again at each start until it is
switched off. Given a port alone it binds loopback, and the tunnel or TLS
proxy that makes it public reaches it there, so it needs no firewall rule
either. Name a wider address only for a proxy on another
machine.

### Checking it worked

- `curl -s https://spool.example.com/api/version` names your build, and
  `curl http://<server address>:8080` from another machine does not connect.
- Sign in at `https://spool.example.com`. The browser's cookie for it is
  `HttpOnly; Secure; SameSite=Strict`.
- `sudo systemctl restart spool` brings the control room back within
  seconds, and `sudo reboot` brings it back without you.

### The cloud listener on the control room's name

A host that already serves the control room through Caddy can make the
cloud listener public on the same name, with no tunnel. Route the cloud
listener's three paths to its port, 8090 unless you changed it, and
everything else to the control room. This replaces the site block in
[TLS with Caddy](#tls-with-caddy):

```caddyfile
spool.example.com {
	@cloud path /mcp /mcp/check/* /hooks
	handle @cloud {
		reverse_proxy 127.0.0.1:8090
	}
	handle {
		reverse_proxy 127.0.0.1:8080
	}
}
```

Reload Caddy, and paste `https://spool.example.com` as the Public address.
The control room serves none of those paths, so routing them away takes
nothing from it. The trade-off: the cloud listener then shares the control
room's origin. The internet reaches both on one name, and a browser sends
the control room's cookie along to the cloud paths, which ignore it. Give
the cloud listener a name of its own, a second site block with only the
`reverse_proxy` to 8090, to keep the two apart.

### A Tailscale Funnel beside Caddy

The cloud setup's Tailscale step runs `tailscale funnel --bg 8090`, with your
cloud listener's port, 8090 unless you changed it. From outside, the funnel
works beside Caddy. From the server itself, the `*.ts.net` name resolves to
the server's own tailnet address, and there Caddy holds 443 with no
certificate for that name. The hub's self-check of the public address runs
from the server, so it fails with
`tls: internal error` while the address works from everywhere else.

Give Caddy the name too, with Tailscale's certificate. Add to
`/etc/caddy/Caddyfile`, with your machine and tailnet names and that port:

```caddyfile
<machine>.<tailnet>.ts.net {
	tls {
		get_certificate tailscale
	}
	reverse_proxy 127.0.0.1:8090
}
```

Then let the caddy user fetch that certificate from tailscaled, and reload:

```bash
echo 'TS_PERMIT_CERT_UID=caddy' | sudo tee -a /etc/default/tailscaled
sudo systemctl restart tailscaled && sudo systemctl reload caddy
```

The cloud setup's Public address step then passes, and says it reached the
listener over a private route from this machine: the request went over the
tailnet, which proves nothing of the Funnel. So test from off the tailnet
too, such as from a phone on mobile data:
`curl -i https://<machine>.<tailnet>.ts.net/mcp` should get the hub's
answer, a 401. If a cloud session still can't reach the hub, its start
turns the step red and holds every cloud loop, until you fix the Funnel
and check the step again (#1093).

**The name doesn't resolve.** `tailscale funnel status` can show the
Funnel as on before public DNS has the `*.ts.net` name. Until it does,
the Public address step fails. Check the name from off the tailnet:
`dig +short <machine>.<tailnet>.ts.net @1.1.1.1`. An empty answer means
the name hasn't reached public DNS yet. Wait a few minutes and check the
step again. If it stays empty, put the cloud listener on
[the control room's name](#the-cloud-listener-on-the-control-rooms-name)
instead.

## Claude logins

The hub runs contained and cloud loops on a *Claude login*: a claude.ai
account it signs in to with the `claude` CLI's own login, and keeps
sealed under the hub key. It refreshes each one before its token expires,
and never puts one in a workstation (ADR-0058, ADR-0060).

A hub can hold several Claude logins, each under a name. The one named
`default` is the one **Sign in with Claude** signs in, on the setup card
and in Settings → Cloud. A contained loop runs on the login it names, its
`claude_login`, and one that names none on `default`. A bare loop runs on
this machine's own login, and a cloud loop on `default` until a later
release (#1078), so naming a login on either is refused (409). Moving a
loop to another login closes its process at its next quiet boundary, as
an attached env-var does, and its session goes on. A loop on a login
that is signed out is held until the login is signed in again or the
loop moves: it never falls back to `default`.
Until `default` is signed in, a stored setup-token stands in for it.
Each login keeps its own plan usage, which only its own loops report
into, and `GET /api/plan-usage` lists it under `logins`. The plan cap
works per login too: a login over a threshold sleeps only the loops on
it, until that login's windows reset, and the two thresholds apply to
every login. Resume now names the login it lifts
(`POST /api/plan-cap/resume` with `{"claude_login": "<name>"}`, the
default with none). Bare loops follow the `default` login's cap, since
they report into its usage, and so do cloud loops. Moving a capped loop
to a login that isn't capped wakes it. With more than one login, the
Fleet page's plan strip shows a ring for each, named, whose figures
open on a click (#1074). A capped login's ring wears a red rim in place
of a cap banner, and its figures name the loops on it that sleep, with a
Resume now for that login alone (#1076).

**Whose subscription a login is.** Each login is the subscription of the
person who signs it in, and it runs the loops they run. Spool never pools
or resells capacity. The operator is responsible for following the terms
of each plan.

**Who may do what.** Any user may add a login of their own, and becomes
its owner. Its owner or an admin may sign it in, check it, sign it out
and remove it. `default` is the hub's, so it is an admin's. Every
user sees each login's name, owner and whether it is signed in, but the
address it signed in with only on the logins they may manage. Removing a
user signs out the logins they own, revoked at the source, and keeps
them, so the loops on them keep a valid name. `spool user remove` has no
hub to revoke them with, so it only forgets their tokens and names the
logins to revoke at claude.ai. With the hub running, remove the user
from the control room instead: a refresh under way could write a
rotated token back over what the command cleared.

**In the control room**, Settings → Claude logins lists them, the
default first, each with its state and last check. **Add a Claude
login** takes a name, then runs the same sign-in as the default. Each
login's line carries Sign in, Check again, Sign out and Remove for its
owner and the admins, and Sign out first names the loops it holds
(#1070). New loop picks a contained loop's login, default preselected,
and the loop's Workstation card names the login it runs on; there an
admin moves a contained loop to another, which takes effect from its
next turn (#1072).

**From the API**:
- `GET /api/claude-logins` lists them, with each one's last check and,
  on the logins the caller may manage, the address it signed in with;
  never a token.
- `POST /api/claude-logins` with `{"name": "ada"}` adds one, signed out.
  Then `POST /api/claude-logins/ada/sign-in` answers with the link to
  open, and `POST /api/claude-logins/ada/sign-in/code` with `{"code": …}`
  hands back the code claude.ai shows. Each sign-in waiting for its code
  runs the CLI on the hub's host for up to ten minutes, so a user may
  have one waiting and the hub four; past either the start answers 429.
- `POST /api/claude-logins/ada/check` checks it with one haiku answer, on
  the docker runtime the way a contained loop would run.
  Checking `default` checks the cloud account too, whose result is the
  note under the default's row and on the Cloud page.
- `POST /api/claude-logins/ada/sign-out` revokes it at the source and
  keeps it, so the loops on it keep a valid name. Sign out is never
  refused.
- `DELETE /api/claude-logins/ada` removes it. It is refused while loops
  run on it, and `default` counts every loop that names none.
- An admin puts a loop on a login with `"claude_login": "ada"` in the
  loop's create or `PATCH`, and `"default"` or `""` moves it back. A
  member changes no loop's config. Put a loop on another user's login
  only with that user's say-so: nothing enforces it until loops have
  owners (#100).

## Cloud loops

A cloud loop's claude runs in a Claude Code cloud session on Anthropic's
machines, on the hub's Claude login, and reaches the hub over the internet
(ADR-0059). Settings → Cloud walks the hub's setup in three steps, in this
order. Each step the hub checks before the next opens. The hub needs the
`claude` CLI on its machine, the same one the Claude login signs in with.

1. **Cloud listener.** Switch it on. On Spool's side, the hub opens the
   cloud listener on `127.0.0.1:8090`, the one listener a cloud session can
   reach. It serves only the cloud loops' MCP and the posts their sessions'
   hooks make. Nothing changes on claude.ai.
2. **Public address.** Put a tunnel or a TLS proxy in front of the
   listener ([TLS with Caddy](#tls-with-caddy),
   [the control room's name](#the-cloud-listener-on-the-control-rooms-name),
   [a Tailscale Funnel](#a-tailscale-funnel-beside-caddy)) and paste its
   HTTPS address. On Spool's side, the hub sends a request through the
   address and checks that its own listener answered. It checks again every
   five minutes, and just before a cloud loop wakes. Nothing changes on
   claude.ai.
   - A request from the hub's own machine can take a route no cloud
     session can, such as a tailnet's. When it went to a private address,
     the step says so: test the address from outside your network too.
   - The cloud session has the last word. A session whose start never
     reaches the hub turns the step red and holds every cloud loop. The
     hub's own checks don't clear that; **Check again** on the step does,
     once the address works from outside.
3. **Claude login.** Sign in with Claude, or keep the login the hub
   already has. On Spool's side, the hub checks the login with one haiku
   answer. The account is the one every cloud loop spends.

Then set each cloud loop up, in a cloud environment of its own that you
create on claude.ai for it. Its setup view, `GET
/api/loops/<name>/cloud/setup`, shows what to paste and which steps are
left: `settings`, `repo`, `environment`, `token`, `allowlist` and `check`.
The loop's page walks those steps: **Set up** on its Workstation card,
which New loop opens on a new cloud loop (#1183). The API takes each step
too.

The list to paste into an environment's allowed domains is the hub's
egress allowlist ([CONTAINMENT.md](CONTAINMENT.md)) as an environment
spells it, with the public address's host first: without that host the
session never loads the hub's MCP server. An entry an environment can't
hold, such as a host with a port of its own, is named beside the list and
left out of it. The walk lists the Claude login's environments by name,
with one read-only call; if the list fails, the loop takes a pasted ID.
The steps, in order:

1. **Its repo.** Create the loop with the cloud runtime and its GitHub repo,
   `owner/name`, its branch, `main` unless you name another, and any further
   repos it attaches. Install Claude's GitHub App on those repositories
   only, and review the installations of the GitHub user behind the
   Claude login at `github.com/settings/installations`: the session can
   reach every repository they cover.
2. **Its environment.** On claude.ai, at
   [claude.ai/code](https://claude.ai/code), open the environment selector
   under the message box and choose **New environment**. Under **Network
   access**, choose **Custom**, paste the list into **Allowed domains**,
   and leave **Also include default list of common package managers**
   unchecked. Paste the setup view's `script` as its **Setup script**: the
   same text for every cloud loop of the hub, holding no secret. Name the
   environment's ID on the loop, `cloud_environment_id`.
3. **Its token.** Rotate token, `POST /api/loops/<name>/cloud/token`,
   stops the loop and shows its hub token once. Set it as the
   environment's network secret for the setup view's `secret_host`, then
   start the loop. The hub keeps only the token's hash, so a lost token is
   rotated, not read back.
4. **Its allowed domains.** Once the list is pasted, confirm it with
   `POST /api/loops/<name>/cloud/allowlist/confirm`, naming the view's
   `digest`. Until you do, the loop carries the warning that the hub's
   egress allowlist doesn't bind it, and an edit to the hub's allowlist
   asks for the list again.
5. **Its check.** `POST /api/loops/<name>/cloud/check`, with the loop
   started, starts one short session in the environment with a message
   that asks nothing of it, and the setup view reads how it went. It
   passes when that session's `SessionStart` post reaches the hub. A
   failed check names where it stopped: `environment`, the CLI refused
   the start, as for an environment the Claude login doesn't have, with
   its last line; `token`, the session reached the hub without the loop's
   token, so the network secret is missing or wrong; `unreached`, the hub
   heard nothing, so the public host isn't in the allowed domains or the
   setup script isn't the hub's. The session is never the loop's, gets
   none of its connections, and stays on claude.ai, where you can
   archive it.

A cloud loop whose repo, environment or token isn't set, or whose check
hasn't passed on its setup as it is now, waits with a `cloud_setup`
problem, and so does one made before ADR-0061, whose old token the upgrade
revoked. An edit of its repo, branch or environment, a Rotate token or a
kill takes a new check. A loop's repo, branch and environment change
only while it is stopped.

The setup script also denies the session the `claude-code-remote` tools that
act on the claude.ai account, such as `create_trigger` and `list_sessions`, and
lets `add_repo` attach only the loop's own repos. Both are guards a root shell
in the VM can undo, not a boundary. Stop, kill and Rotate token end no routine
or session a loop made on claude.ai, so after killing a loop that misbehaved,
check the account's routines and sessions there.

At its first wake, the hub runs `claude --cloud` on its own machine, with
the wake as the first message, in an empty directory whose `origin` is the
loop's repo, with `--ref` naming its branch and the loop's environment.
Nothing is uploaded: the session clones the repo from GitHub, its
environment's setup script connects it to the hub, and its `SessionStart`
hook posts the session's id. The hub takes the post only from the session
the CLI printed it had created, answers it with the loop's prompt, and ends
the local CLI. The loop's page links the session, which you can open on
claude.ai. When the turn ends, the `Stop` hook posts it back, and the
loop's reply lands in its rooms as any loop's does. Each later wake is
sent into the same session with `claude -p --cloud`. Anthropic idles the
session's machine between turns, and the next message brings it back.
Changing the public address takes one more step: paste the new setup
script into each loop's environment, and its host into the allowed
domains, then stop and start each cloud loop, which drops its session, and
check it again. A check stands for the address it ran against, so one
that passed before the change counts again if the address goes back.

A cloud loop's hub token belongs to its environment, not to one session.
It is kept at Anthropic as that environment's network secret, with no
expiry: anyone who holds the claude.ai account can start a session there
that carries it, though the hub binds the loop's wakes only to the
session its own start created. Rotate token is how to end it while
keeping the loop.
Stopping a cloud loop refuses its token at once, and starting it again
takes the same token, with nothing to set again. Killing it revokes the
token for good: the loop waits, with the `cloud_setup` problem, until
Rotate token mints another and you set it. Either way the CLI can't
interrupt a turn already running there: it runs on, cut off from the hub,
and so may a message sent just before the stop. The hub no longer hears
that turn end, so the loop's page says it may still be running until two
hours after its send, and links its session, where you can interrupt it.
If the hub can't store the stop or the revocation, the stop or the kill
still happens, but it answers with an error saying the session can still
reach the hub. Stopping or killing the loop again retries.

A cloud loop's secrets are its env-var connections, attached as a
contained loop's are. The hub answers the session's
`SessionStart` post with them, and the hook writes them where the
session's shell commands read them. A change to a
connection reaches the loop at its next session start, not its next turn:
stop and start the loop to apply it now. If Anthropic reclaims the idle
machine, the session keeps them. A post the hub refuses raises a problem on the
loop, saying its shell may be running without them. The values stay in
the session on Anthropic's disk until you archive it on claude.ai, so
deleting a cloud loop that misbehaved is the time to rotate them.
`mcp-server` connections never reach a cloud loop.

A cloud loop's GitHub work goes through Claude's GitHub App, from the repo
it starts from (ADR-0061). Its requests go through Anthropic's GitHub
proxy, which refuses GraphQL, so `gh pr` and `gh issue` fail and only REST
through `gh api` works. A push can update a branch, never push a tag or
delete a branch. A `GH_TOKEN` connection reaches no repository the session
didn't start from or attach (#1169).

A cloud turn's cost is the hub's estimate. The session reports its token
counts but no cost, so the hub prices the tokens by its own table of model
prices. A model missing from that table leaves the turn's cost unknown, and
the day's total says it leaves that turn out. Cloud turns don't report the
plan's usage limits either: the hub reads those from contained loops only,
so a hub with only cloud loops has no plan usage to show.

## Back up and restore

The commands here assume the layout of [Run it as a service](#run-it-as-a-service):
the hub runs as a system unit named `spool` with its data directory at
`/home/spool/.spool`, and you run them as the checkout's owner, with `sudo`
on the lines that need root. A hub you start by hand (`./bin/spool`, data in
`~/.spool`) is stopped and started however you run it, and gets its own
`--data-dir`.

### What a backup holds

A hub's state is in two places: its data directory (`--data-dir`, default
`~/.spool`) and one docker volume per contained loop, `spool-ws-<loop id>`,
which holds the loop's sessions, memory, workspace and the tools it installed.
A backup that copies only the directory brings every docker loop back with
no memory.

`scripts/spool-backup.sh` writes three files:

| File | Holds | Keep it |
|---|---|---|
| `spool-data-<ts>.tar.gz` | the data directory without its two credentials or an unfinished cloud sign-in (`spool.db`, `files/`, `homes/`, `worktrees/`) | with your backups |
| `spool-volumes-<ts>.tar.gz` | every `spool-ws-*` volume, and a manifest naming each volume's loop; none with `--no-volumes` | with your backups |
| `spool-keys-<ts>.tar` | `hub.key` and `operator-token` | **apart** from the data file, e.g. in a password manager or on another host |

Every secret in `spool.db` is sealed under `hub.key` (ADR-0046). The data
file without the keys file holds them only as ciphertext, and grants no API
access. Keep the two apart and a leaked copy of either is not enough. The
volumes file is still sensitive: a workstation volume holds whatever its
loop holds, its own GitHub token included. All three files are written 0600.

Install `pigz` (`sudo apt install pigz`) and the backup and the restore
compress on every core; without it, `gzip` uses one (#948). A backup from
before #948 holds its volumes inside the data file, and the restore still
reads it that way.

A volume is found by its name, `spool-ws-<loop id>`, not by its labels. A
volume made by hand, as when loops move over from another machine, has no
labels, and the hub runs on it all the same. The backup lists such a
volume as `no loop labels` and takes its loop id from the name. A restore
creates it with the labels, so later backups list it by its loop (#844). A
script from before that change skipped such volumes without a word.

### Installing the scripts

Run the scripts from a copy installed outside the checkout, never from
`scripts/` itself. An upgrade or a rollback moves the checkout to a tag,
and tags from before the scripts (v0.5.1 and earlier) don't have them: a
restore run from there would stop with the data directory already moved
aside. Install them from `main`, whatever tag the checkout is on, and do it
again at every upgrade. Run git as the checkout's owner and let `sudo` do
the writes outside it. A `sudo` in front of a `>` redirect doesn't help, because
your own shell opens the file:

```bash
cd /opt/spool && git fetch origin
sudo install -d -m 700 /var/backups/spool    # where the backups below are written
for s in spool-backup.sh spool-restore.sh; do
  git show "origin/main:scripts/$s" | sudo install -m 755 /dev/stdin "/usr/local/sbin/$s"
done
```

The scripts need nothing else from the checkout. The commands below call
them from `/usr/local/sbin`.

### Taking one

The hub must be stopped. `spool.db` runs in WAL mode, so a copy taken under a
running hub can miss its latest commits, and a loop that is awake is writing
to its volume. The script refuses when a process holds `spool.db` open. It
stops running workstation containers for the copy and starts them again
afterwards. It reads volumes through the local `spool-workstation` image and
never pulls one.

Run it as the hub's user or as root. The open-file check sees only the
caller's processes, so run as anyone else it would pass against a running
hub.

```bash
sudo systemctl stop spool                  # or stop the hub however you run it
sudo /usr/local/sbin/spool-backup.sh --data-dir /home/spool/.spool --out /var/backups/spool
sudo systemctl start spool
```

Loops are paused while the copy runs: seconds for a small fleet, longer once
volumes hold installed toolchains. A nightly
line for root's crontab, with the hub as a system unit named `spool`:

```cron
15 3 * * *  systemctl stop spool && /usr/local/sbin/spool-backup.sh --data-dir /home/spool/.spool --out /var/backups/spool; systemctl start spool
```

The `;` before `start` restarts the hub even when the backup fails. Move the
keys file off the host. Prune old backups yourself, data and volumes files
both. The script deletes nothing.

A hub with only bare loops, or no docker on the host, passes `--no-volumes`.
A bare loop's sessions live under the hub user's `~/.claude/projects/`, which
this script does not copy.

### Restoring

```bash
sudo /usr/local/sbin/spool-restore.sh --data spool-data-<ts>.tar.gz \
  --volumes spool-volumes-<ts>.tar.gz \
  --keys spool-keys-<ts>.tar --data-dir /home/spool/.spool
sudo systemctl start spool                 # or start the hub however you run it
```

A backup that wrote no volumes file (one taken with `--no-volumes`, or on a
docker hub with no workstation volume yet) restores without `--volumes`.

Run as root, restore gives every file it writes to the owner the backup
recorded, the hub's user, keys included (#783). Restore never overwrites
anything. The data directory must be missing or empty, and no volume the
backup names may exist yet. All checks run before anything is written. To
restore over a broken hub, move its data directory aside, and remove
each volume the refusal names along with the workstation container of
the same name. Docker refuses to remove a volume a container still uses,
even a stopped one:

```bash
docker rm -f spool-ws-<id> && docker volume rm spool-ws-<id>
```

The containers need nothing more: the hub provisions one on a loop's next
wake and reuses the restored volume. Build the images first (`make image`) on a
new host.

Check it worked:

- The hub starts. A sealed database with a missing or wrong `hub.key` makes
  it exit 1 at once, saying so.
- `curl -H "Authorization: Bearer $(cat ~/.spool/operator-token)" http://127.0.0.1:8080/api/loops`
  returns your loops.
- A docker loop answers a message and remembers what it knew.
- Access is what the backup held, not what you last set. Any user, link,
  allowed sender or block changed after the backup was taken is back to its
  old state, so a person removed since then can reach the loops again. Check
  Settings → Users and access before you let the fleet run.

**Lost the keys file?** Restore without `--keys`. The hub refuses to start
until `hub.key` is back. Starting it once with `--forget-secrets` gives up
every sealed secret instead: connections are revoked, bots unbound and the
setup-token cleared, so you enter them again (ADR-0046). A new
`operator-token` is minted at that start. Under the systemd unit its
stdout goes nowhere, so read the token with `spool token`.

A restored `worktrees/` points at each loop's source repository by path. If
that repository moved, run `git worktree repair` in it.

## Upgrade

An upgrade moves the checkout to a new tag, rebuilds the binary and the
images, and starts the hub on the same data directory. The hub migrates
`spool.db` forward at its first start. No release migrates it back, so the
backup taken just before that start is the way back. Take it every time.

### With `spool upgrade`

```bash
sudo spool upgrade --dry-run       # the plan, with the unit's paths; changes nothing
sudo spool upgrade                 # the latest release; or vX.Y.Z, or main
```

It does the steps below in their order (ADR-0057). It reads the binary,
the data directory and the images from the unit (`--unit`, default
`spool`), and checks the new build before anything stops. A release is
checked by its checksum, and by `gh attestation verify` run as you if you
have `gh` and are logged in; without that it warns, and
`--require-attestation` makes it refuse. A target that doesn't know a
migration the database has applied is refused. It then stops the hub,
backs up the data directory with the installed `spool-backup.sh
--no-volumes` into `/var/backups/spool` (`--backup-dir`, which must lie
outside the data directory and is checked before anything stops), installs,
starts, and waits two minutes for the hub to answer at the target
version. If it doesn't, the command rolls the binary, the images and
the data directory back, leaves the volumes, and prints where the
backup and the failed data directory are. Each step prints as it
starts, with the time since the run began.

The upgrade's backup leaves out the workstation volumes. The upgrade
changes none, and a rollback restores none, so copying them would only
keep the hub down longer. So it isn't a full backup: keep the
[scheduled one](#back-up-and-restore) running for the volumes.

Before it builds or pulls, the upgrade tags the images the hub runs as
`spool-workstation:spool-previous` and `spool-egress:spool-previous`, and
a rollback tags them back. They stay until the next upgrade moves them;
`docker image rm` them to reclaim the space once the new hub is checked.

`main`, or a tag with `--from-source <checkout>`, is built in the
checkout as its owner. Until a release ships binaries (#747), only those
work. The installed backup and restore scripts are needed either way:
[Installing the scripts](#installing-the-scripts).

The build runs in the owner's login shell, which isn't interactive, so it
reads `~/.profile` and not `~/.bashrc`. It loads a Node or Go that nvm,
fnm, volta or asdf installed in its default place, and finds Go in
`/usr/local/go/bin`. Before anything stops, even on `--dry-run` under
`sudo`, it checks that `git`, `make`, `go`, `node`, `npm` and `docker` are
there and that the owner can use docker without sudo. It names whatever is
missing. A tool installed anywhere else needs its `PATH` set in
`~/.profile`. Check what the build sees with:

```bash
sudo runuser -l "$USER" -c 'command -v go; command -v node; command -v npm'
```

The steps [by hand](#by-hand) are the fallback, and the reference for
what the command does.

### By hand

The commands assume the checkout is at `/opt/spool` and the hub runs as a
system unit named `spool`. Substitute your own checkout. The binary the unit
runs and the data directory are read from the unit itself (step 1 below), so
a hub set up some other way than [Run it as a service](#run-it-as-a-service)
upgrades the same. Run them as the user who owns the checkout, who
can also run `docker`, as `make image` needs. `sudo` marks the lines that
need root: the unit, the scripts, and anything under `/usr/local/sbin` or
`/home/spool`. Running the whole block as root instead leaves root's files
in your checkout, and git refuses a checkout root doesn't own.

### Before you start

1. **Find what the unit runs.** The upgrade replaces that binary and backs
   up that data directory, wherever they are. Run this in the shell you
   upgrade from:

   ```bash
   unit=$(systemctl show spool -p ExecStart)
   BIN=$(printf '%s' "$unit" | sed -n 's/.*path=\([^ ;]*\).*/\1/p')
   DATA=$(printf '%s' "$unit" | sed -n 's/.*--data-dir[= ]\([^ ;]*\).*/\1/p')
   echo "binary: ${BIN:?no unit named spool}  data: ${DATA:?no --data-dir in the unit; set DATA by hand}"
   ```

   With the unit above, these are `/opt/spool/bin/spool` and
   `/home/spool/.spool`. A unit that runs a copy elsewhere, such as
   `/usr/local/bin/spool`, is why the block below installs the new build: a
   rebuilt `bin/spool` alone changes nothing the unit starts.
2. **Read what changed.** Note the running build with `"$BIN" --version`.
   Read every release's notes between that build and the target on the
   [releases page](https://github.com/enes-alatas/spool/releases). Then check
   [Release-specific steps](#release-specific-steps) below for anything
   between your version and the target.
3. **Keep the build you are leaving**, so a rollback needs no rebuild:

   ```bash
   cd /opt/spool
   git describe --tags --always > bin/previous-version
   cp "$BIN" bin/spool.previous
   ```
4. **Check the build tools** if you build from source rather than
   downloading a release. The running hub needs neither Go nor Node; only
   `make build` does. Make sure your own shell has them, at the versions the
   README names:

   ```bash
   go version && node --version && npm --version
   ```

   Don't fall back to `make server`. It skips the web UI and embeds whatever
   older build is left in `web/dist`.

### Upgrading

```bash
cd /opt/spool
git fetch --tags origin
for s in spool-backup.sh spool-restore.sh; do   # refresh the installed scripts first
  git show "origin/main:scripts/$s" | sudo install -m 755 /dev/stdin "/usr/local/sbin/$s"
done
git checkout vX.Y.Z                         # the target tag
make build                                  # web UI + bin/spool; the running hub is unaffected
make image                                  # spool-workstation and spool-egress, rebuilt locally

sudo systemctl stop spool
sudo /usr/local/sbin/spool-backup.sh --data-dir "$DATA" --out /var/backups/spool
# Install the build where the unit runs it; a unit that runs bin/spool in place needs nothing.
[ "$BIN" -ef bin/spool ] || sudo install -m 755 bin/spool bin/spool-hook "$(dirname "$BIN")/"
# spool-github, for a bare loop holding a github-app (#1054); a build from before it has none.
[ "$BIN" -ef bin/spool ] || [ ! -f bin/spool-github ] || sudo install -m 755 bin/spool-github "$(dirname "$BIN")/"

# Only for a target of v0.5.1 or earlier, whose hub doesn't notice a rebuilt
# image (#715): remove the containers, never the volumes, and the hub
# provisions each from the new image at its loop's next wake, on the same
# volume. A later hub replaces them itself at that wake.
docker rm -f $(docker ps -aq --filter label=spool.loop.id) spool-egress-proxy

sudo systemctl start spool
```

**To main, between releases.** Replace the tag line with
`git checkout main && git pull --ff-only origin main`. `git fetch` alone
moves `origin/main`, not your `main`, so checking out `main` after it
builds the commit you already run. `git describe --tags` then names the
build, such as `v0.5.1-159-g8691220`.

Do not use the control room's **Recreate workstation** for this. It removes
the volume too, and with it the loop's memory and workspace.

Building happens while the old hub still runs, so the outage is only the
backup and the start.

**From a release.** A release from the one after v0.5.1 on carries the built
binary (#756, ADR-0052), so `make build` above can be replaced with a
download. A release whose notes have an **Images** section also carries the
images (#763), so `make image` can be replaced with what the new binary's
`spool images` prints. Each image is pulled by digest, which is its check,
and tagged with the name the hub runs, so the hub replaces the workstations
at their next wake, as it does after `make image`. Like `make image`, that
moves the loops of the hub that is still running. To keep them on the old
images until the new hub starts, run `./bin/spool images | sh` after
`systemctl stop spool` instead. The rest of the block stays the same.

```bash
v=vX.Y.Z; p=linux_amd64                      # the target tag; linux_arm64 on ARM
base=https://github.com/enes-alatas/spool/releases/download/$v
dl=$(mktemp -d) && cd "$dl"                  # private to you: nothing swaps the file after the check
curl -fsSLO "$base/spool_${v}_${p}.tar.gz" -O "$base/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS     # the file is the release's
gh attestation verify "spool_${v}_${p}.tar.gz" --repo enes-alatas/spool   # optional: the release workflow built it
tar -xzf "spool_${v}_${p}.tar.gz"
install -m 755 "spool_${v}_${p}/spool" "spool_${v}_${p}/spool-hook" /opt/spool/bin/
[ ! -f "spool_${v}_${p}/spool-github" ] || install -m 755 "spool_${v}_${p}/spool-github" /opt/spool/bin/   # a release from before #1054 has none
cd /opt/spool && rm -rf "$dl"                # back for the rest of the block

./bin/spool images                           # read it: two pulls by digest, two tags
./bin/spool images | sh                      # run them; running loops move at their next wake
```

The checksum catches a broken download. The attestation also catches a
file and checksums that were swapped together, so run it if you have `gh`.
An image's attestation is checked the same way:
`gh attestation verify oci://<image>@sha256:… --owner enes-alatas`, with the
references `spool images` prints.

To try a release before upgrading to it, run `scripts/release-smoke.sh
vX.Y.Z` from the clone on a throwaway VM with Docker. It installs the
release the README's way, checks the checksum and attestations, and starts a
hub on its images with a data directory of its own. Never run it on the
fleet's host: it retags `spool-workstation` and `spool-egress`, and it
refuses a daemon that already runs a hub's containers. The release
workflow runs the same check after every tag (#770).

### Checking it worked

- `curl -s http://127.0.0.1:8080/api/version` names the target tag, and
  `curl -s http://127.0.0.1:8080/api/health` answers `{"ok":true}`. That
  is the hub's answer. A loop names the build its session started on until
  its next rotation, so a loop still quoting the old build proves nothing.
- `journalctl -u spool -n 100` shows `runtime ready`, and no `level=ERROR` since the
  start.
- Sign in to the control room. Every loop is listed, and the Fleet page shows
  no loop failing.
- Message one docker loop and wait for its answer. That wake is what
  provisions its container from the new image. `docker ps --filter
  label=spool.loop.id` then lists it again.

### Rolling back

To roll back, return to the previous build and restore the backup taken
during the upgrade. Anything the fleet did after the upgrade (messages,
memory, loop changes) is lost.

Starting the old binary on the migrated `spool.db` is not supported. The old
binary skips migrations it doesn't know, then reads tables in a shape it was
never written for.

`$BIN` and `$DATA` are the ones step 1 of [Before you start](#before-you-start)
read; read them again in a new shell.

```bash
sudo systemctl stop spool
cd /opt/spool
git checkout "$(cat bin/previous-version)"
cp bin/spool.previous bin/spool
[ "$BIN" -ef bin/spool ] || sudo install -m 755 bin/spool.previous "$BIN"
make image                                  # the previous release's images

sudo mv "$DATA" "$DATA.failed-upgrade"
docker rm -f $(docker ps -aq --filter label=spool.loop.id) spool-egress-proxy
docker volume rm $(docker volume ls -q --filter name=spool-ws-)   # by name: a volume may carry no labels (#844)
sudo /usr/local/sbin/spool-restore.sh --data /var/backups/spool/spool-data-<ts>.tar.gz \
  --volumes /var/backups/spool/spool-volumes-<ts>.tar.gz \
  --keys /var/backups/spool/spool-keys-<ts>.tar --data-dir "$DATA"

sudo systemctl start spool
```

`docker volume rm` destroys the volumes as they stood after the upgrade. Run
it only once you have the backup's data and volumes files in hand. A backup
taken before #948 has no volumes file: leave out `--volumes`, and the
restore reads the volumes from the data file. Keep
`$DATA.failed-upgrade` until the rolled-back hub is checked, then delete it:
it holds the same credentials as the live directory.

The restore runs from `/usr/local/sbin`, so it works whatever the previous
build is, including one from before the scripts existed. Don't refresh the
installed copy during a rollback.

If the new hub never started (a failed build, or a stop before `systemctl
start`), the database was not migrated. Putting `bin/spool.previous` back
where the unit runs it (`$BIN`) and starting is then enough.

### Release-specific steps

Steps a release needs on top of the above, newest first. A change that alters
stored data at its first start adds its row here in the same PR (see
CONVENTIONS.md, Releases).

| Release | At its first start | What it means for you |
|---|---|---|
| v0.6.0 | Rewrites nothing. A loop's owner is now chosen as a hub user, and stored as that user's link on the loop's surface (#731). | Link your Telegram, or your Slack, in Settings → Account before changing any loop's owner. A user with no link there cannot be chosen. Senders allowed before the upgrade keep working, and keep the loops they own, shown as not linked. |
| v0.5.0 | Mints `hub.key` and seals every stored secret in `spool.db` under it, rewriting the database (ADR-0046). | The pre-upgrade backup is the only copy of the database a v0.4 binary can read. From now on `hub.key` must be in every backup's keys file. A hub without it will not start. |
