# Langfuse spike runbook (#1187)

What the operator types on the hub's machine to trace one contained loop into
a Langfuse of their own: way 1 (Langfuse's Stop hook) for a day, then way 2
(Claude Code's OpenTelemetry traces) for a day. Nothing in Spool changes, and
the hub runs as before throughout. Each file here was proved on a fresh runner
first; the evidence is on #1187.

Needs on the hub's machine: Docker with compose v2, `curl`, `jq`, `openssl`,
and a DNS name for Langfuse's ingestion, below `langfuse.example.com`, pointing
at the machine your Caddy already serves the hub from. Replace `<loop id>` and
`<loop name>` with the loop's, from its page.

## 1. Fetch and start Langfuse

```sh
mkdir -p ~/langfuse && cd ~/langfuse
b=https://raw.githubusercontent.com/enes-alatas/spool/<commit>/spike/langfuse
for f in compose.langfuse.yml make-env.sh langfuse-report.sh lf-install.sh; do curl -fsSLO "$b/$f"; done
chmod +x ./*.sh
LANGFUSE_PROJECT=way1 ./make-env.sh                 # langfuse.env, 0600, every secret generated
docker compose -f compose.langfuse.yml --env-file langfuse.env --profile langfuse up -d
curl -s http://127.0.0.1:3000/api/public/health      # {"status":"OK",...} after about a minute
```

Without `--profile langfuse`, compose starts nothing. Langfuse listens on
`127.0.0.1:3000` only, with telemetry off and sign-up off.

## 2. Let the loop reach ingestion, and nothing else

Add a site to your Caddyfile that passes only the OpenTelemetry ingestion
through, then `sudo systemctl reload caddy`:

```caddyfile
langfuse.example.com {
	@ingest path /api/public/otel/* /api/public/health
	handle @ingest {
		reverse_proxy 127.0.0.1:3000
	}
	handle {
		respond 404
	}
}
```

Then, in the control room, **Settings → Egress**: add `langfuse.example.com`.
This list is the fleet's, so every docker loop can reach the name. Only the
loop you give the keys to can send, and Langfuse refuses the rest.

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
     --entrypoint sh spool-workstation /spike/lf-install.sh way1 https://langfuse.example.com <loop name>
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
docker compose -f compose.langfuse.yml --env-file langfuse.env --profile langfuse down -v
rm langfuse.env && LANGFUSE_PROJECT=way2 ./make-env.sh
docker compose -f compose.langfuse.yml --env-file langfuse.env --profile langfuse up -d
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
     --entrypoint sh spool-workstation /spike/lf-install.sh way2 https://langfuse.example.com <loop name>
   ```

3. After a day, report as in way 1, into `way2-report.txt` and
   `way2-footprint.txt`.

## 6. Teardown

```sh
docker run --rm -u loop -e HOME=/home/loop -v spool-ws-<loop id>:/home/loop -v "$PWD":/spike:ro \
  --entrypoint sh spool-workstation /spike/lf-install.sh remove
docker compose -f compose.langfuse.yml --env-file langfuse.env --profile langfuse down -v
rm langfuse.env
```

Then remove the loop's Langfuse connection, the `langfuse.example.com` egress
entry and the Caddy site. `down -v` deletes every trace. Way 1's traces can
hold anything a tool printed, so delete them rather than keep them.
