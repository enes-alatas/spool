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
# `systemd-journal` can read, so it goes nowhere instead.
StandardOutput=null
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
The loop listener (`--mcp-listen`) binds the docker bridge (README, "What
contains a loop"), which the network is not routed to. A default-deny inbound
policy can still drop the workstations' own traffic to it, since that
traffic arrives from the bridge. The symptom is a docker loop that never
answers while its wake times out in the hub log. The fix is to allow that one
port from the bridge only:

```bash
sudo ufw allow in on docker0 to any port 8081 proto tcp
```

### Checking it worked

- `curl -s https://spool.example.com/api/version` names your build, and
  `curl http://<server address>:8080` from another machine does not connect.
- Sign in at `https://spool.example.com`. The browser's cookie for it is
  `HttpOnly; Secure; SameSite=Strict`.
- `sudo systemctl restart spool` brings the control room back within
  seconds, and `sudo reboot` brings it back without you.

## Back up and restore

### What a backup holds

A hub's state is in two places: its data directory (`--data-dir`, default
`~/.spool`) and one docker volume per contained loop, `spool-ws-<loop id>`,
which holds the loop's sessions, memory, workspace and the tools it installed.
A backup that copies only the directory brings every docker loop back with
no memory.

`scripts/spool-backup.sh` writes two files:

| File | Holds | Keep it |
|---|---|---|
| `spool-data-<ts>.tar.gz` | the data directory without its two credentials (`spool.db`, `files/`, `homes/`, `worktrees/`), every `spool-ws-*` volume, and a manifest naming each volume's loop | with your backups |
| `spool-keys-<ts>.tar` | `hub.key` and `operator-token` | **apart** from the data file, e.g. in a password manager or on another host |

Every secret in `spool.db` is sealed under `hub.key` (ADR-0046). The data
file without the keys file holds them only as ciphertext, and grants no API
access. Keep the two apart and a leaked copy of either is not enough. The
data file is still sensitive: a workstation volume holds whatever its loop
holds, its own GitHub token included. Both files are written 0600.

### Installing the scripts

Run the scripts from a copy installed outside the checkout, never from
`scripts/` itself. An upgrade or a rollback moves the checkout to a tag,
and tags from before the scripts (v0.5.1 and earlier) don't have them: a
restore run from there would stop with the data directory already moved
aside. Install them from `main`, whatever tag the checkout is on, and do it
again at every upgrade. As root:

```bash
cd /opt/spool && git fetch origin
install -d -m 700 /var/backups/spool         # where the backups below are written
for s in spool-backup.sh spool-restore.sh; do
  git show "origin/main:scripts/$s" > "/usr/local/sbin/$s" && chmod 755 "/usr/local/sbin/$s"
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
systemctl stop spool                       # or stop the hub however you run it
/usr/local/sbin/spool-backup.sh --data-dir ~/.spool --out /var/backups/spool
systemctl start spool
```

Loops are paused while the copy runs: seconds for a small fleet, longer once
volumes hold installed toolchains. A nightly
line for root's crontab, with the hub as a system unit named `spool`:

```cron
15 3 * * *  systemctl stop spool && /usr/local/sbin/spool-backup.sh --data-dir /home/spool/.spool --out /var/backups/spool; systemctl start spool
```

The `;` before `start` restarts the hub even when the backup fails. Move the
keys file off the host. Prune old data files yourself. The script deletes
nothing.

A hub with only bare loops, or no docker on the host, passes `--no-volumes`.
A bare loop's sessions live under the hub user's `~/.claude/projects/`, which
this script does not copy.

### Restoring

```bash
/usr/local/sbin/spool-restore.sh --data spool-data-<ts>.tar.gz \
  --keys spool-keys-<ts>.tar --data-dir ~/.spool
./bin/spool --data-dir ~/.spool              # or systemctl start spool
```

Restore never overwrites anything. The data directory must be missing or
empty, and no volume the backup names may exist yet. All checks run before
anything is written. To restore over a broken hub, move its data directory
aside, and remove each volume the refusal names along with the workstation
container of the same name. Docker refuses to remove a volume a container
still uses, even a stopped one:

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
  Settings → Users and the Access page before you let the fleet run.

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

The commands assume the checkout is at `/opt/spool`, the hub runs as a
system unit named `spool`, and the data directory is `/home/spool/.spool`.
Substitute your own paths.

### Before you start

1. **Read what changed.** Note the running build with `./bin/spool --version`.
   Read every release's notes between that build and the target on the
   [releases page](https://github.com/enes-alatas/spool/releases). Then check
   [Release-specific steps](#release-specific-steps) below for anything
   between your version and the target.
2. **Keep the build you are leaving**, so a rollback needs no rebuild:

   ```bash
   cd /opt/spool
   git describe --tags --always > bin/previous-version
   cp bin/spool bin/spool.previous
   ```

### Upgrading

```bash
cd /opt/spool
git fetch --tags origin
for s in spool-backup.sh spool-restore.sh; do   # refresh the installed scripts first
  git show "origin/main:scripts/$s" > "/usr/local/sbin/$s" && chmod 755 "/usr/local/sbin/$s"
done
git checkout vX.Y.Z                         # the target tag
make build                                  # web UI + bin/spool; the running hub is unaffected
make image                                  # spool-workstation and spool-egress, rebuilt locally

systemctl stop spool
/usr/local/sbin/spool-backup.sh --data-dir /home/spool/.spool --out /var/backups/spool

# Only for a target of v0.5.1 or earlier, whose hub doesn't notice a rebuilt
# image (#715): remove the containers, never the volumes, and the hub
# provisions each from the new image at its loop's next wake, on the same
# volume. A later hub replaces them itself at that wake.
docker rm -f $(docker ps -aq --filter label=spool.loop.id) spool-egress-proxy

systemctl start spool
```

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
cd /opt/spool && rm -rf "$dl"                # back for the rest of the block

./bin/spool images                           # read it: two pulls by digest, two tags
./bin/spool images | sh                      # run them; running loops move at their next wake
```

The checksum catches a broken download. The attestation also catches a
file and checksums that were swapped together, so run it if you have `gh`.
An image's attestation is checked the same way:
`gh attestation verify oci://<image>@sha256:… --owner enes-alatas`, with the
references `spool images` prints.

### Checking it worked

- `curl -s http://127.0.0.1:8080/api/version` names the target tag, and
  `curl -s http://127.0.0.1:8080/api/health` answers `{"ok":true}`.
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

```bash
systemctl stop spool
cd /opt/spool
git checkout "$(cat bin/previous-version)"
cp bin/spool.previous bin/spool
make image                                  # the previous release's images

mv /home/spool/.spool /home/spool/.spool.failed-upgrade
docker rm -f $(docker ps -aq --filter label=spool.loop.id) spool-egress-proxy
docker volume rm $(docker volume ls -q --filter label=spool.loop.id)
/usr/local/sbin/spool-restore.sh --data /var/backups/spool/spool-data-<ts>.tar.gz \
  --keys /var/backups/spool/spool-keys-<ts>.tar --data-dir /home/spool/.spool

systemctl start spool
```

`docker volume rm` destroys the volumes as they stood after the upgrade. Run
it only once you have the backup's data file in hand. Keep
`.spool.failed-upgrade` until the rolled-back hub is checked, then delete it:
it holds the same credentials as the live directory.

The restore runs from `/usr/local/sbin`, so it works whatever the previous
build is, including one from before the scripts existed. Don't refresh the
installed copy during a rollback.

If the new hub never started (a failed build, or a stop before `systemctl
start`), the database was not migrated. Restoring `bin/spool.previous` and
starting is then enough.

### Release-specific steps

Steps a release needs on top of the above, newest first. A change that alters
stored data at its first start adds its row here in the same PR (see
CONVENTIONS.md, Releases).

| Release | At its first start | What it means for you |
|---|---|---|
| v0.5.0 | Mints `hub.key` and seals every stored secret in `spool.db` under it, rewriting the database (ADR-0046). | The pre-upgrade backup is the only copy of the database a v0.4 binary can read. From now on `hub.key` must be in every backup's keys file. A hub without it will not start. |
