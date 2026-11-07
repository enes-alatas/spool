# ADR-0064: Loops trace to the operator's own Langfuse, docker loops through the egress proxy

Date: 2026-11-04 · Status: accepted (operator decision of 2026-11-05, in a direct message to Terra, recorded on #1310, accepting the ADR; operator decision of 2026-11-03, in a direct message to Elliot, recorded on #1187: way 2 with every `OTEL_LOG_*` off, and the relay in the egress proxy; operator decision of 2026-11-04, in a direct message to Terra, recorded on #1310: bare loops export straight to Langfuse with the key in their environment) · Amended: 2026-11-05 (item 5: langfuse-web's networks, #1299; accepted by the operator on 2026-11-05, in a direct message to Elliot, recorded on #1315); 2026-11-07 (item 3: the hub's Langfuse file, #1388) · Relates to: ADR-0013, ADR-0028, ADR-0043, ADR-0046, ADR-0053, ADR-0059

## Context

The Activity page shows what the loops said, not how they are doing. An
operator can't see a loop's spend per turn, how long its turns take,
which tools it reaches for or where it fails without reading every
message (#1294). Langfuse is a self-hostable tracing store with an
OpenTelemetry (OTLP) endpoint and read APIs for observations and metrics.

The spike on #1187 tried two ways on one loop, each on a fresh Langfuse
4.71.0 that was torn down afterwards:

- **Way 1, Langfuse's Stop hook.** It sends the whole turn: user and
  assistant text, thinking, tool inputs and outputs, and the text of
  `send_message` calls, owner DMs included. All of it bypasses the hub's
  redactor, so a credential a tool printed lands in Langfuse in plain
  text. It also needs Python in every workstation and a hook merged
  beside Spool's. MinIO held 76 MB after a day.
- **Way 2, Claude Code's own OpenTelemetry traces, with content off.**
  It needs no package or hook, only settings and one header. A turn
  arrives as a trace of about 19 observations (`AGENT`, `GENERATION`,
  `SPAN`, `TOOL`):
  - each generation carries its model, its input, output and cache tokens,
    its price, `ttft_ms`, the stop reason and the request ID;
  - each tool span carries the tool's name, `success`, `duration_ms`
    and the permission decision;
  - each turn span carries `interaction.duration_ms`.

  That answers spend, turn duration, tool mix and failures (`level`,
  `error_class`). No prompt, response, tool input or tool output arrived;
  `user_prompt` is the `<REDACTED>` placeholder. What does leave is
  `bash_argv0` (a program name such as `git`), `bash_command_class`, tool
  and MCP server names, `user.id` (an opaque account hash), session and
  conversation IDs, and the host's arch and OS. MinIO held 0.56 MB, about
  40 KB a turn.

Way 2 broke in one place: Claude Code 2.1.316's OTLP trace exporter
ignores `HTTP(S)_PROXY`. A contained loop reaches nothing but the egress
proxy (ADR-0028), so traces arrived only once a forwarder inside the
workstation relayed them through it. A Spool product built on way 2
needs such a relay. It is new surface, and this ADR places it.

## Decision

1. **Way 2: Claude Code's native trace export, content off.** A
   docker or bare loop's session runs with `CLAUDE_CODE_ENABLE_TELEMETRY=1`,
   `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1`, `OTEL_TRACES_EXPORTER=otlp`,
   and the metrics and logs exporters set to `none`, since Langfuse takes
   traces only. A docker loop's traces go to the relay (item 2), a bare
   loop's straight to Langfuse (item 4). Every `OTEL_LOG_*`
   is off:
   - `OTEL_LOG_USER_PROMPTS`
   - `OTEL_LOG_ASSISTANT_RESPONSES`
   - `OTEL_LOG_TOOL_DETAILS`
   - `OTEL_LOG_TOOL_CONTENT`
   - `OTEL_LOG_RAW_API_BODIES`
   - `OTEL_LOG_MANAGED_SETTINGS`

   These are the names in 2.1.316; one a later CLI adds is off too.
   The hub sets these variables itself, after a loop's connections and
   the hub's own environment, so neither a connection (ADR-0043) nor
   the hub's environment turns one on. The loop's own settings can: it
   writes its home, and the `env` block of its Claude Code settings
   reaches its next session. Item 6 covers that. With the hub's
   settings, a trace carries what the context above lists, and nothing
   of what a loop read or wrote. The docker
   workstation image's `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`
   stays. The trial ran with it, and it doesn't stop this export.
   Way 1 is not built.

2. **The relay is in the egress proxy.** `spool-egress` gains one OTLP/HTTP
   listener on each loop network, the proxy's only listener other than
   the egress proxy itself. A loop's endpoint is that listener's
   `/v1/traces`, at the proxy's name.
   - **One path.** It takes `POST /v1/traces` and nothing else. Any other
     method or path is refused, and so is a request whose content
     encoding is anything but identity.
   - **One upstream.** It forwards to the one upstream the operator set
     (item 3), at its OTLP path (`/api/public/otel/v1/traces`), and
     follows no redirect. The upstream is https, or `http://langfuse-web`,
     the compose profile's web container (item 5), which sits on the
     egress network. With no upstream set, the relay refuses every
     request and holds no key.
   - **The auth header is the relay's.** It sets `Authorization` from the
     ingest key (item 3) and drops any `Authorization` the loop sent.
     It forwards `Content-Type`, and no other header the loop set.
   - **The loop's identity is stamped.** The relay knows which loop's
     network a request came in on, and each network holds one
     workstation and no NET_RAW (ADR-0028), so nothing can forge it. It
     sets `spool.loop.id` and `spool.loop.name` on every resource in the
     request, overwriting whatever the loop sent. It removes any
     `spool.loop.*` attribute from scopes and spans, since Langfuse shows
     those beside the resource's. A trace that names another loop is
     rewritten, not refused, so a misbehaving loop shows up under its own
     name. A trace is attributed to a loop by that resource stamp alone,
     never by `service.name`, `user.id` or a session ID, which the loop
     writes. That includes the hub's read side (#1300). For the relay
     to read and rewrite the request, the loop exports `http/json`
     (`OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`), and the relay refuses any
     other content type. That keeps the relay on `encoding/json`, with no
     protobuf dependency. The trial exported `http/protobuf`, so the
     relay's slice (#1296) checks first that 2.1.316 sends JSON and that
     Langfuse takes it.
   - **Bounds.** It takes a body of at most 4 MiB and 60 requests a
     minute per loop, and answers 413 or 429 past either. Claude Code
     batches its spans, every 5 s by default, and a turn is about 40 KB,
     so neither bound is near a working loop. Time and concurrency are
     bounded as well:
     - the listener has read and header timeouts;
     - the upstream call has a timeout of its own;
     - a loop has at most a few requests in flight, and gets 429 past
       that.

     So a slow upstream can't make one loop hold the proxy's
     connections.
   - **Status only, back to the loop.** The relay answers with the
     upstream's status code and nothing else: no body, no header from
     Langfuse. So a loop can't read Langfuse through the relay, even in an
     error.
   - **The relay is the only way in.** The proxy's own path refuses a
     CONNECT or a forward to the hosts of both Langfuse addresses (item 3),
     and to `langfuse-web`, whatever the allowlist says. An allowlist entry
     can't reopen Langfuse's read API.

   **Docker loops hold no Langfuse key.** Nothing about Langfuse but the
   relay's address is in a docker loop's environment. This replaces the
   trial's per-loop header connection.

3. **The keys live in the hub, sealed.** Settings takes two addresses and
   two keys:
   - **The relay's upstream:** where the proxy sends traces, as the
     proxy reaches Langfuse. With the profile (item 5) that is
     `http://langfuse-web`, which resolves only on the egress network,
     and it is the default.
   - **The read address:** where the hub reads figures back, as the hub
     reaches Langfuse. With the profile on a native hub that is the UI
     port on `127.0.0.1`. It is https, or http on loopback.
   - **An ingest key and a read key.**

   A Langfuse the operator runs elsewhere has one https address, which
   goes in both fields. The keys are sealed in `spool.db` under
   `hub.key` like every other secret the hub stores (ADR-0046). The
   settings API reports only whether a key is set, never its value.
   Both keys join the redactor's secrets (`redact.StoreSource`), and so
   does the `Authorization` value the hub builds for a bare loop, the
   base64 of the ingest key's `public:secret` pair, which masking the
   key alone doesn't catch. A bare loop that prints its environment in
   a turn then shows neither in a transcript, the Activity page or a
   chat. The bare-loop slice (#1313) tests that a message carrying
   either is masked.
   - The hub hands the ingest key to the proxy, and to each bare loop
     (item 4), the only other holders. For the proxy, the key never sits
     unsealed where a copy of the data directory, `docker inspect` or the
     host's disk reaches. The proxy has two existing channels. Its run
     arguments show in `docker inspect`. The fleet file is copied into
     its container filesystem, which docker keeps on the host's disk. So
     the key takes neither. It isn't in the proxy's env either. The hub
     runs `docker exec` into the proxy with the relay's configuration on
     stdin. The proxy keeps it in a tmpfs it re-reads, and the hub hands
     it again whenever it provisions the proxy or the key changes. #1296
     builds that channel. If it finds a better one, it amends this item.
   - The read key stays in the hub, which reads figures back for the
     control room (#1300).
   - Langfuse doesn't scope a key pair. Either pair can read every trace in
     its project, so the two fields may hold the same pair. Neither
     reaches a docker loop; the ingest key reaches a bare loop.

   **Amendment (2026-11-07, #1388):** with the compose profile, the hub
   learns the settings from a file the setup script writes, not from the
   operator. The control room holds no Langfuse settings (operator
   decision of 2026-11-07, recorded on #1298), so without this the
   settings API would be the only way to turn traces on.
   - **The setup script writes two files** (item 5). One is the
     profile's env file, holding every secret compose reads. The other
     is the hub's Langfuse file, `docker/langfuse/hub.env`, holding the
     project's key pair alone. Both are 0600, and the script never
     overwrites either.
   - **The hub reads its own file only.** The profile's env file also
     holds the Langfuse admin's password and the datastores' passwords.
     Every bare loop runs as the hub's user, so a file the hub could
     read would give each bare loop those too. The hub's file holds
     nothing a bare loop doesn't already reach. The operator gives it
     to the hub's user, and keeps the env file from that user.
   - **At start the hub reads the hub's Langfuse file.** By default that
     is `docker/langfuse/hub.env` in the checkout whose `bin/` holds the
     binary, not the working directory, which a unit sets to the hub
     user's home. `--langfuse-env` names another path.
   - **A file that is there sets all four settings:** the upstream
     `http://langfuse-web`; the read address, which is `LANGFUSE_URL`
     if the operator added one, or else the UI port
     `http://127.0.0.1:3000`; and
     `LANGFUSE_PUBLIC_KEY:LANGFUSE_SECRET_KEY` as both keys. The hub
     stores them sealed like any other settings, and marks them as the
     file's. The redactor's first load, after the read, takes the keys.
   - **The file wins while it is there.** The settings API refuses to
     change settings the file set, naming the file, since the next start
     would undo the change. Settings stored through the API, for a
     Langfuse the operator runs elsewhere, apply only while there is no
     file.
   - **A file the hub can't use stops it.** That covers a line that isn't
     `NAME=VALUE`, a missing key, or a `LANGFUSE_URL` that breaks this
     item's rule. The refusal names the file and the line or variable,
     never a value. A file the hub's user may not read only warns, and
     the stored settings stay: Langfuse is optional, and the fleet runs
     without it.
   - **No file is no change,** except that settings an earlier start
     took from a file are cleared, and the hub logs that it did. That is
     traces off at each loop's next session start, and it adds an off
     switch beside item 5's: remove the file and restart the hub. Either
     way, a pair let go of stays redacted.

4. **Bare loops export straight to Langfuse; cloud loops export nothing.**
   - **A bare loop** runs on the host with no wall and no loop network.
     It is the operator's local code, trusted as the hub's own user is,
     so it holds the key the relay holds for a docker loop. The relay
     can't tell which bare loop a request came from, so a bare loop
     doesn't use it. Its session gets item 1's variables and:
     - an endpoint at the read address (item 3), at Langfuse's OTLP
       path, since a bare loop reaches Langfuse as the hub does. It
       follows item 3's rule, https or http on loopback, so the key
       never crosses a network in clear, and the hub sets no endpoint
       for an address that breaks it;
     - `OTEL_EXPORTER_OTLP_TRACES_HEADERS` carrying the ingest key as
       the `Authorization` header;
     - the same `http/json` protocol a docker loop exports (item 2), so
       #1296's check covers both;
     - `OTEL_RESOURCE_ATTRIBUTES` set to the loop's `spool.loop.id` and
       `spool.loop.name`, the stamp the relay writes for a docker loop.

     The hub drops every `OTEL_*` variable it inherited and sets these
     after the connections, as item 1 does, so a hub started from a
     shell that exports an OTLP endpoint doesn't send its bare loops
     there. Unless the upstream, the read address and the ingest key
     are all set, the hub sets `CLAUDE_CODE_ENABLE_TELEMETRY=0` and no
     endpoint. A changed key or
     address reaches a bare loop at its next session start. Its slice
     (#1313) checks first that 2.1.316 puts
     `OTEL_RESOURCE_ATTRIBUTES` on its trace resources.

     A bare loop differs from a docker loop in three ways, which the
     operator accepted on 2026-11-04 (#1310):
     - It holds a key that reads every trace in the project: every
       loop's figures, and content wherever item 6 turns it on.
     - Its stamp is the hub's setting, not a wall. It can rewrite the
       variable, or post spans in another loop's name, so a bare loop's
       attribution is trusted no further than the loop.
     - Item 2's bounds don't apply: it reaches Langfuse with no relay
       between.
   - **A cloud loop** runs in a Claude Code cloud session on Anthropic's
     machines (ADR-0059), which can't reach the operator's Langfuse. Its
     data says `langfuse: unsupported`.
   - The Langfuse card in Settings names which loops export: docker loops
     through the relay, bare loops straight to Langfuse.

5. **Langfuse runs as an optional `langfuse` compose profile.** The
   profile is off by default, and the hub runs unchanged without it. It
   holds Langfuse's six containers: web, worker, ClickHouse, Postgres,
   Redis and MinIO. It is Spool's first compose file, and it lives at
   `docker/langfuse/compose.yml`, beside the images' own directories.
   The hub still makes its own networks and containers at run time.
   - **Footprint:** about 2.6 GiB of RAM idle, which is Langfuse's own and
     not the traffic's, and about 40 KB of disk a turn.
   - **Images** are pinned by digest, as the hub's are.
   - **Secrets** are generated into a 0600 env file at setup, and
     never committed. Setup refuses to overwrite an existing file.
     Signup is off, and the org, the project and its key pair come from
     Langfuse's `LANGFUSE_INIT_*` variables.
   - **Langfuse's own telemetry is off** (`TELEMETRY_ENABLED=false`, which
     upstream defaults on). Its AI features are off too: no provider key
     is set, so Langfuse sends nothing to a model.
   - **Ports:** only the UI's, on `127.0.0.1`. The UI sits behind its own
     login, and behind Caddy if the operator exposes it.
   - **Networks:** `langfuse-web` joins the egress network only, never a
     loop network, since a loop network carries the loop's hub token in
     plain HTTP.
     - The egress network is the hub's, made by the docker runtime
       (ADR-0028) and named after the proxy image (`spool-egress` by
       default). Compose joins it as an external network, named by a
       variable that defaults to `spool-egress`, and never creates or
       removes it.
     - It exists once a hub with the docker runtime has started. Before
       that the profile doesn't come up, and its setup says to start the
       hub first.
     - ClickHouse, Postgres, Redis and MinIO sit on an internal-only
       `langfuse` network with no route out.

     **Amendment (2026-11-05, #1299):** the networks are as follows.
     - **The egress network is the only Spool network `langfuse-web`
       joins.** It also joins two of the profile's own:
       - the internal `langfuse` network, where it reaches the
         datastores;
       - `langfuse-ui`, a plain bridge network with nothing else on
         it, which carries the published UI port.
     - **Why `langfuse-ui`:** docker publishes no port for a container
       whose networks are all internal, and the egress network is
       internal (ADR-0028). So without a non-internal network there is no
       UI on `127.0.0.1` for the operator or for the hub's read address.
     - **What it costs:** `langfuse-web` has a route out of its own, as
       upstream's compose file gives it. Its telemetry and AI features
       stay off, as above.
     - **The worker** sits on `langfuse` alone, beside the datastores,
       with no route out.
     - **No other port** is published, and no Spool container joins
       `langfuse-ui`.
   - **Not backed up.** Langfuse's volumes are outside
     `spool-backup.sh`. They hold telemetry that rebuilds from nothing.
   - **The off switch:** clearing the upstream in Settings turns every
     loop's export off at its next session start, bare loops' included
     (item 4), and leaves the relay refusing. Bringing the profile down
     removes Langfuse itself. With both done, no loop sends anywhere.
     A bare loop that copied the ingest key into its home or a file
     keeps it, so with bare loops the switch also deletes or rotates
     the key pair in Langfuse. A changed key reaches only a bare loop's
     new sessions, so a rotation does the same.

6. **Content is a separate decision.** Turning any `OTEL_LOG_*` on is an
   operator decision of its own, recorded as an amendment to this item.
   From then on Langfuse holds what the loops read, credentials included,
   and needs the care `spool.db` gets. Every bare loop can read it too,
   docker loops' content included, through the ingest key (item 4). None
   of it is on.

   "Off" is the hub's configuration, not a wall. A loop runs its own
   shell, so it can post spans with any content to the relay, as it can
   to any host on its allowlist, and a bare loop straight to Langfuse.
   What Langfuse holds is therefore written by the loop, and is trusted
   no further than the loop. A docker loop's identity stamp (item 2) is
   the one field it can't write; a bare loop can write its own (item 4).

7. **This is not telemetry in QUALITY's sense.** "No telemetry, ever"
   means the binary never phones home (ADR-0013). An operator's own
   Langfuse, opt-in and self-hosted, isn't that: the traces go to a host
   the operator set, and nowhere else. QUALITY says so in those words.

## Consequences

- An operator who turns Langfuse on sees, for each docker and bare loop,
  its spend and tokens per turn, its turn durations, its tool mix and its
  failures, in Langfuse and then on the Activity page (#1300, #1301).
  None of it comes with a loop's content.
- The egress proxy gains its first listener that isn't the proxy, and its
  first upstream credential. The relay's slice (#1296) adds both to
  THREAT-MODEL's egress section, with Elliot's security review.
- Every docker loop on a hub with Langfuse set reaches the relay. A loop
  can write spans, and only spans. It can't read Langfuse through the
  relay, and its spans carry its own identity.
- The trial's fleet-wide allowlist entry for `langfuse-web` is not needed,
  and the proxy refuses one (item 2). Langfuse's read API stays
  unreachable from docker loops.
- Every bare loop on a hub with Langfuse set holds the ingest key, which
  reads every loop's traces. Its own traces are attributed by a stamp it
  could rewrite. The bare-loop slice (#1313) adds both to THREAT-MODEL, with
  Elliot's security review.
- Cloud loops show nothing in Langfuse, and the Settings card says so.
- A Claude Code release can change what its traces carry or what a
  `OTEL_LOG_*` covers. The FIDELITY rows for the export record the
  version they were checked on, and a new content setting is off until
  item 6 says otherwise.
- The hub depends on Langfuse's OTLP path and read APIs. Langfuse being
  down loses traces, never turns. The exporter drops what it can't send,
  and the relay keeps nothing.
- The health check (ADR-0053) keeps reading the hub's own turns. Nothing
  it decides rests on Langfuse, which is opt-in and loop-written.

## Alternatives considered

- **Way 1, Langfuse's Stop hook.** It gives the richest view of a turn,
  but it sends the whole transcript past the redactor, DMs and any
  credential a tool printed included. It is safe only for a loop that
  holds no secret, and only where every Langfuse reader may read all of
  that loop's conversations. Rejected.
- **A forwarder inside each workstation**, as in the trial. The loop
  would hold the key, or the forwarder would be loop-controlled code
  stamping its own identity. Rejected for the proxy, which the loop
  doesn't control.
- **A relay in the hub, on the loop listener.** That listener carries
  the hub token's MCP traffic. A relay there would let trace volume
  share a listener with the hub's control path, and would give the hub
  the ingest key on a path loops call. The proxy is already a loop's one
  way out. Rejected.
- **Bare loops export nothing**, as this ADR first proposed, since a bare
  loop would hold a key that reads every trace and the relay can't tell
  bare loops apart. The operator rejected it on 2026-11-04 (#1310): a
  bare loop is already the operator's local risk, and leaving it out
  gives away the view for every loop of a bare-only hub.
- **A relay in the hub for bare loops**, naming each by the hub token it
  already holds. It would keep the key out of bare loops and make their
  stamp unforgeable. Not built now: it is the loop-listener relay above,
  for loops already trusted with the host. A later ADR can add it.
- **Spool's own store only.** The hub already has per-turn cost, tokens
  and duration (`turns`). Langfuse adds the inside of a turn: each model
  call, its latency and tokens, the tool spans, and the API-error and
  compaction events. Spool's own figures stay. Langfuse is opt-in on
  top of them.
