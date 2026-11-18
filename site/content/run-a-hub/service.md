---
title: Run it as a service
weight: 1
---

These steps take a fresh Debian or Ubuntu server to a hub that starts at
boot, restarts on failure, and serves the control room over HTTPS at a name
you own. The other Run a hub pages assume this layout:

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

## The hub's user

```bash
sudo useradd --system --create-home --home-dir /home/spool --shell /usr/sbin/nologin spool
sudo usermod -aG docker spool
```

Membership of `docker` is what lets the hub run workstations. It is also
root on this host to anyone who can act as `spool`, which is why nothing
but the hub runs as that user.

## The unit

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
# The hub's stdout carries only the operator token and the admin's
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

## TLS with Caddy

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

## The firewall

Open 80 and 443 for Caddy, and SSH. Open nothing for the hub.

```bash
sudo ufw allow OpenSSH && sudo ufw allow 80,443/tcp && sudo ufw enable
```

The control room port is on loopback, so the network cannot reach it at all.
The loop listener (`--mcp-listen`) binds the docker bridge
([CONTAINMENT.md](https://github.com/enes-alatas/spool/blob/main/docs/CONTAINMENT.md)), which the network is not routed to. A default-deny inbound
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

## Checking it worked

- `curl -s https://spool.example.com/api/version` names your build, and
  `curl http://<server address>:8080` from another machine does not connect.
- Sign in at `https://spool.example.com`. The browser's cookie for it is
  `HttpOnly; Secure; SameSite=Strict`.
- `sudo systemctl restart spool` brings the control room back within
  seconds, and `sudo reboot` brings it back without you.

## The cloud listener on the control room's name

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

## A Tailscale Funnel beside Caddy

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
