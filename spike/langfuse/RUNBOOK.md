# Langfuse spike runbook (#1187)

What the operator types on the hub's machine to trace one contained loop into
a Langfuse of their own: way 1 (Langfuse's Stop hook) for a day, then way 2
(Claude Code's OpenTelemetry traces) for a day. Nothing in Spool changes, and
the hub runs as before throughout. Each file here was proved on a fresh runner
first; the evidence is on #1187.

Needs on the hub's machine: Docker with compose v2, `curl`, `jq` and `openssl`.
Replace `<loop id>` and `<loop name>` with the loop's, from its page.

## 1. Fetch and start Langfuse

```sh
mkdir -p ~/langfuse && cd ~/langfuse
b=https://raw.githubusercontent.com/enes-alatas/spool/<commit>/spike/langfuse
for f in compose.langfuse.yml compose.spool-egress.yml make-env.sh langfuse-report.sh lf-install.sh; do curl -fsSLO "$b/$f"; done
chmod +x ./*.sh
LANGFUSE_PROJECT=way1 ./make-env.sh                 # langfuse.env, 0600, every secret generated
docker compose -f compose.langfuse.yml -f compose.spool-egress.yml --env-file langfuse.env --profile langfuse up -d
curl -s http://127.0.0.1:3000/api/public/health      # {"status":"OK",...} after about a minute
```

Without `--profile langfuse`, compose starts nothing. On the host, Langfuse
publishes `127.0.0.1:3000` only, with telemetry off and sign-up off.

## 2. Let the loops reach it through the egress proxy

`compose.spool-egress.yml` puts Langfuse's web container on the egress proxy's
own network, as `langfuse-web`. No loop's network gets it, since a loop's
network carries that loop's hub token in plain HTTP. The proxy reaches
Langfuse by that name, so there's no public name, Caddy site or TLS. Inside
its container Langfuse listens on port 80, the port a control-room entry
without a port opens.

The overlay expects the network to be called `spool-egress`, which is the
hub's default. Check the name before step 1's `up`:

```sh
docker network ls --filter label=spool.egress=network --format '{{.Name}}'   # spool-egress
```

If it prints another name, put `SPOOL_EGRESS_NETWORK=<that name>` in
`langfuse.env`.

Then, in the control room, **Settings → Egress**: add `langfuse-web`. This
list is the fleet's, so every docker loop can reach the whole Langfuse API,
the read API included. Only the key pair keeps them out, and only the loop
you give it to holds it. If the hub ever recreates its egress network, run
step 1's `up` again to rejoin it.

The Langfuse UI stays on loopback. Open it over SSH from your own machine:
`ssh -L 3000:127.0.0.1:3000 <hub machine>`, then <http://localhost:3000>,
signing in as `admin@example.com` with `LANGFUSE_ADMIN_PASSWORD` from
`langfuse.env`.

## 3. Way 1: Langfuse's Stop hook, for a day

Pick a loop holding **no connection but its GitHub token**: this way uploads
the whole transcript, tool outputs included, and the hub's redaction never sees
that path.

1. **Connections:** add two env-var connections from `langfuse.env`, attached
   to this loop only:
   - `LANGFUSE_PUBLIC_KEY`, set to the file's `LANGFUSE_PUBLIC_KEY`;
   - `LANGFUSE_SECRET_KEY`, set to the file's `LANGFUSE_SECRET_KEY`.
2. **Pause** the loop.
3. **Install** into its home. This pins uv 0.13.6, Python 3.12, `langfuse` 4.18.0
   and the hook at plugin commit `b1619f9`, each checked by hash. It writes
   `~/.claude/settings.json` beside what is there:

   ```sh
   docker run --rm -u loop -e HOME=/home/loop -v spool-ws-<loop id>:/home/loop -v "$PWD":/spike:ro \
     --entrypoint sh spool-workstation /spike/lf-install.sh way1 http://langfuse-web <loop name>
   ```

4. **Resume** the loop and leave it a day. The first turn should show a trace
   in the UI. If none comes, the hook disables itself without its keys, so
   check the connections first.
5. **Report**, then paste the output on #1187. It prints field names, types and
   lengths, never values:

   ```sh
   set -a; . ./langfuse.env; set +a
   LANGFUSE_URL=http://127.0.0.1:3000 ./langfuse-report.sh 1000 > way1-report.txt
   docker stats --no-stream --format '{{.Name}} {{.MemUsage}}' | grep langfuse > way1-footprint.txt
   docker system df -v | grep langfuse_ >> way1-footprint.txt
   ```

## 4. Between the ways: wipe and start again

Way 2 gets a project and keys of its own, so neither way's key reads the
other's data:

```sh
docker run --rm -u loop -e HOME=/home/loop -v spool-ws-<loop id>:/home/loop -v "$PWD":/spike:ro \
  --entrypoint sh spool-workstation /spike/lf-install.sh remove
docker compose -f compose.langfuse.yml -f compose.spool-egress.yml --env-file langfuse.env --profile langfuse down -v
rm langfuse.env && LANGFUSE_PROJECT=way2 ./make-env.sh
docker compose -f compose.langfuse.yml -f compose.spool-egress.yml --env-file langfuse.env --profile langfuse up -d
```

Delete the loop's two Langfuse connections.

## 5. Way 2: Claude Code's OpenTelemetry traces, for a day

Content stays redacted: prompts, responses, tool inputs and tool outputs are
all off by default. Turning on any `OTEL_LOG_*` is a separate choice, made on
#1187 first.

1. **Connection:** add one env-var connection, attached to this loop only:
   `OTEL_EXPORTER_OTLP_TRACES_HEADERS`, set to the output of

   ```sh
   . ./langfuse.env; echo "Authorization=Basic $(printf %s "$LANGFUSE_PUBLIC_KEY:$LANGFUSE_SECRET_KEY" | base64 -w0)"
   ```

2. **Pause**, then install, then **resume**:

   ```sh
   docker run --rm -u loop -e HOME=/home/loop -v spool-ws-<loop id>:/home/loop -v "$PWD":/spike:ro \
     --entrypoint sh spool-workstation /spike/lf-install.sh way2 http://langfuse-web <loop name>
   ```

3. After a day, report as in way 1, into `way2-report.txt` and
   `way2-footprint.txt`.

## 6. Teardown

```sh
docker run --rm -u loop -e HOME=/home/loop -v spool-ws-<loop id>:/home/loop -v "$PWD":/spike:ro \
  --entrypoint sh spool-workstation /spike/lf-install.sh remove
docker compose -f compose.langfuse.yml -f compose.spool-egress.yml --env-file langfuse.env --profile langfuse down -v
rm langfuse.env
```

Then remove the loop's Langfuse connection and the `langfuse-web` egress
entry. `down -v` deletes every trace. Way 1's traces can
hold anything a tool printed, so delete them rather than keep them.
