# Operating a hub

*How to keep a hub running once it is up. Running it as a service, backing
it up and upgrading it have moved to [the docs site](https://enes-alatas.github.io/spool/run-a-hub/); their
headings stay here so old links still land. The README gets a hub started.*

## Run it as a service

Moved to [Run it as a service](https://enes-alatas.github.io/spool/run-a-hub/service/) on the docs site.

### The hub's user

Moved to [The hub's user](https://enes-alatas.github.io/spool/run-a-hub/service/#the-hubs-user) on the docs site.

### The unit

Moved to [The unit](https://enes-alatas.github.io/spool/run-a-hub/service/#the-unit) on the docs site.

### TLS with Caddy

Moved to [TLS with Caddy](https://enes-alatas.github.io/spool/run-a-hub/service/#tls-with-caddy) on the docs site.

### The firewall

Moved to [The firewall](https://enes-alatas.github.io/spool/run-a-hub/service/#the-firewall) on the docs site.

### Checking it worked

Moved to [Checking it worked](https://enes-alatas.github.io/spool/run-a-hub/service/#checking-it-worked) on the docs site.

### The cloud listener on the control room's name

Moved to [The cloud listener on the control room's name](https://enes-alatas.github.io/spool/run-a-hub/service/#the-cloud-listener-on-the-control-rooms-name) on the docs site.

### A Tailscale Funnel beside Caddy

Moved to [A Tailscale Funnel beside Caddy](https://enes-alatas.github.io/spool/run-a-hub/service/#a-tailscale-funnel-beside-caddy) on the docs site.

## Claude logins

The hub runs contained and cloud loops on a *Claude login*: a claude.ai
account it signs in to with the `claude` CLI's own login, and keeps
sealed under the hub key. It refreshes each one before its token expires,
and never puts one in a workstation (ADR-0058, ADR-0060).

A hub can hold several Claude logins, each under a name. The one named
`default` is the one **Sign in with Claude** signs in, on the setup card
and in Settings → Cloud. A contained loop runs on the login it names, its
`claude_login`. One that names none runs on its team's login, if its
team names one, and otherwise on `default`. A bare loop runs on
this machine's own login, so naming a login on one is refused (409). A
cloud loop names one too, and its environment must be one of that
login's account's ([Cloud loops](#cloud-loops), #1078). Moving a
contained loop to another login closes its process at its next quiet
boundary, as an attached env-var does, and its session goes on. A loop
on a login
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
they report into its usage. A cloud loop follows its own login's, though
its turns report no usage (ADR-0059), so only the login's other loops
move it. Moving a capped loop to a login that isn't capped wakes it.
With more than one login, the Fleet page's plan strip shows a ring for
each, named, whose figures open on a click (#1074). A capped login's ring wears a red rim in place
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
(#1070). New loop picks a contained or cloud loop's login, default
preselected, and the loop's Workstation card names the login it runs
on; there an admin moves a contained loop to another, which takes
effect from its next turn (#1072). A cloud loop moves only while
paused, and one with an environment picks its new one from the new
login's account's list in the same move; it then needs a new hub token
and a passed check before it runs again (#1228).

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
- `DELETE /api/claude-logins/ada` removes it. It is refused while a team
  names it or loops run on it, and `default` counts every loop on no
  other login.
- An admin puts a loop on a login with `"claude_login": "ada"` in the
  loop's create or `PATCH`, and `"default"` or `""` moves it back. A
  member changes no loop's config. Put a loop on another user's login
  only with that user's say-so: nothing enforces it until loops have
  owners (#100).
- An admin puts a team's contained loops on a login with
  `"claude_login": "ada"` in the team's create or `PATCH`. Each loop that
  names none of its own moves at its next quiet boundary, and a loop
  joining or leaving the team moves with it. A bare loop and a cloud
  loop never take a team's login. The loop view's
  `effective_claude_login` and `claude_login_from` say which login a loop
  runs on and why (ADR-0066).

## Management tokens

A *management token* is the credential an operator's own agent holds to look
after the hub, in place of the operator token (ADR-0067). It opens only
the management MCP, below. An admin mints, lists and revokes them in
Settings → Management tokens (#1403), or over the API:

- `POST /api/management-tokens` with `{"name": "watcher"}` mints one and
  answers with it once, as plain text. Copy it then: the hub keeps only
  its hash and can't show it again. It ends in 90 days; send
  `"expires_at"` as a Unix time in milliseconds for another end, or `0`
  for none.
- `GET /api/management-tokens` lists them by name, owner, when each was made,
  when it ends and when it was last used, never by value.
- `DELETE /api/management-tokens/<id>` revokes one at once: its owner may,
  even once no longer an admin, and so may any admin. Removing a user
  revokes the tokens they minted.

The operator API refuses a management token on every route (401): it is not
a second operator token.

### Pointing an agent at the hub

The management MCP is `/manage/mcp`, streamable HTTP, with the token as a
bearer. It has three tools (ADR-0067):

- `list_loops`: each loop's name, runtime, state, whether it is paused
  and whether `resume_loop` can resume it, its health (`ok`, `warning`
  or `failing`) and when its latest turn started. No mission, message or
  secret.
- `pause_loop` and `resume_loop`, by the loop's name, as the control
  room's Pause and Resume. An agent resumes only a loop an agent paused:
  a person's pause, from the control room or the API, stays theirs to
  lift, and an agent pausing it again doesn't change whose it is.

Each pause and resume is on the loop's change record as
`manage:<token name>#<token id>`.

An agent on the hub's own machine, such as Claude Code in a terminal,
uses the operator listener:

```sh
claude mcp add --transport http spool-manage http://127.0.0.1:8080/manage/mcp \
  --header "Authorization: Bearer spool_manage_…"
```

An agent in a Claude Code cloud session uses the cloud listener's public
address instead, `https://<your address>/manage/mcp`, with the cloud
listener on (see Cloud loops). The loop listener doesn't serve it.

A token revoked or past its end is refused at the agent's next call. A
bare loop can read a token a local agent keeps in your files, as it can
read the operator token (THREAT-MODEL.md).

## Cloud loops

A cloud loop's claude runs in a Claude Code cloud session on Anthropic's
machines, on a Claude login of the hub's, `default` unless the loop
names another, and reaches the hub over the internet (ADR-0059). Settings → Cloud walks the hub's setup in three steps, in this
order. Each step the hub checks before the next opens. The hub needs the
`claude` CLI on its machine, the same one the Claude login signs in with.

1. **Cloud listener.** Switch it on. On Spool's side, the hub opens the
   cloud listener on `127.0.0.1:8090`, the one listener a cloud session can
   reach. It serves only the cloud loops' MCP, the posts their sessions'
   hooks make, and the management MCP. Nothing changes on claude.ai.
2. **Public address.** Put a tunnel or a TLS proxy in front of the
   listener ([TLS with Caddy](https://enes-alatas.github.io/spool/run-a-hub/service/#tls-with-caddy),
   [the control room's name](https://enes-alatas.github.io/spool/run-a-hub/service/#the-cloud-listener-on-the-control-rooms-name),
   [a Tailscale Funnel](https://enes-alatas.github.io/spool/run-a-hub/service/#a-tailscale-funnel-beside-caddy)) and paste its
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
   answer. The account is the one every cloud loop on `default` spends.

Then set each cloud loop up, in a cloud environment of its own that you
create on claude.ai for it. Its setup view, `GET
/api/loops/<name>/cloud/setup`, shows what to paste and which steps are
left: `settings`, `repo`, `environment`, `token`, `allowlist` and `check`.
The loop's page walks those steps: **Set up** on its Workstation card,
which New loop opens on a new cloud loop (#1183). The API takes each step
too, though on a loop made before ADR-0061 the repo and environment go in
one edit, as below.

The list to paste into an environment's allowed domains is the hub's
egress allowlist ([CONTAINMENT.md](CONTAINMENT.md)) as an environment
spells it, with the public address's host first: without that host the
session never loads the hub's MCP server. An entry an environment can't
hold, such as a host with a port of its own, is named beside the list and
left out of it. The walk lists the Claude login's environments by name,
with one read-only call; if the list fails, the loop takes a pasted ID.
A pasted ID is started in only once the list shows it, on every login:
the hub lists the login's environments before each start and setup
check, and a list that doesn't show the loop's environment, or can't
be read, fails the start and names why (#1232). The CLI doesn't refuse
an environment the account doesn't hold, but starts in the account's
first one, so if the environment goes between the list and the start,
the hub ends the CLI and records the loop's check as failed at the
environment: archive any session the check names on claude.ai, name
one of the account's environments, and check again. If you delete or
recreate a loop's environment, stop the loop and name the new ID.
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
revoked. Such a loop has neither a repo nor an environment, and the hub
checks the two together, so they go in one edit: the walk saves the repo
with the environment you pick, and through the API one `PATCH
/api/loops/<name>` names `cloud_repo` and `cloud_environment_id` (#1286).
An edit of its repo, branch, environment or Claude login, a
Rotate token or a kill takes a new check. A loop's repo, branch,
environment and Claude login change only while it is stopped.

**A cloud loop on another Claude login** (#1078). Name the login,
`claude_login`, as for a contained loop. Its sessions start and take
their turns under that login's token, so they spend that login's plan,
and its environment must be one the login's account made: create it on
claude.ai signed in as that account. The walk lists that login's
environments, `GET /api/settings/cloud/environments?login=<name>`, and
an environment the list doesn't show is refused. One pasted while the
list fails is taken, but the loop starts nothing, its check included,
until the list shows it: the hub lists the login's environments before
each start, and a list that doesn't show the loop's environment, or
can't be read, fails the start and names why. Moving a cloud loop to
another login refuses an environment that login's account doesn't
list, drops its session, which belongs to the old account, revokes its
hub token, which the old environment still holds as its secret, and
takes a new check. The loop then waits at step 3: Rotate token, set the
new token as the new environment's secret, and delete the old
environment's secret on claude.ai. Unlike a contained loop's, a cloud
loop's sessions are open to the login's account: whoever holds it
reads every turn, can send into a session and run commands in its VM,
where the loop's `env-var` connections sit. Attach to such a loop only
connections the login's owner may hold.

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
loop's reply lands in its rooms as any loop's does. A turn the API fails,
on a rate limit or a server error, ends with `StopFailure` instead, which
the setup script hooks too: the turn shows as an error at once. An
environment whose script predates that hook still waits two hours with
no post on such a turn: paste the setup view's script into it again. The
setup check stays green either way, but the setup view says the script is
out of date once a session has started under it, and the loop keeps
running meanwhile (#1381). Each
later wake is sent into the same session with `claude -p --cloud`.
Anthropic idles the session's machine between turns, and the next
message brings it back.
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

Moved to [Back up and restore](https://enes-alatas.github.io/spool/run-a-hub/backup/) on the docs site.

### What a backup holds

Moved to [What a backup holds](https://enes-alatas.github.io/spool/run-a-hub/backup/#what-a-backup-holds) on the docs site.

### Installing the scripts

Moved to [Installing the scripts](https://enes-alatas.github.io/spool/run-a-hub/backup/#installing-the-scripts) on the docs site.

### Taking one

Moved to [Taking one](https://enes-alatas.github.io/spool/run-a-hub/backup/#taking-one) on the docs site.

### Nightly, with a timer

Moved to [Nightly, with a timer](https://enes-alatas.github.io/spool/run-a-hub/backup/#nightly-with-a-timer) on the docs site.

### Restoring

Moved to [Restoring](https://enes-alatas.github.io/spool/run-a-hub/backup/#restoring) on the docs site.

## Upgrade

Moved to [Upgrade](https://enes-alatas.github.io/spool/run-a-hub/upgrade/) on the docs site.

### With `spool upgrade`

Moved to [With `spool upgrade`](https://enes-alatas.github.io/spool/run-a-hub/upgrade/#with-spool-upgrade) on the docs site.

### A hub with no unit

Moved to [A hub with no unit](https://enes-alatas.github.io/spool/run-a-hub/upgrade/#a-hub-with-no-unit) on the docs site.

### By hand

Moved to [By hand](https://enes-alatas.github.io/spool/run-a-hub/upgrade/#by-hand) on the docs site.

### Before you start

Moved to [Before you start](https://enes-alatas.github.io/spool/run-a-hub/upgrade/#before-you-start) on the docs site.

### Upgrading

Moved to [Upgrading](https://enes-alatas.github.io/spool/run-a-hub/upgrade/#upgrading) on the docs site.

### Checking it worked

Moved to [Checking it worked](https://enes-alatas.github.io/spool/run-a-hub/upgrade/#checking-it-worked) on the docs site.

### Rolling back

Moved to [Rolling back](https://enes-alatas.github.io/spool/run-a-hub/upgrade/#rolling-back) on the docs site.

### Release-specific steps

Moved to [Release-specific steps](https://enes-alatas.github.io/spool/reference/release-steps/) on the docs site.

## Langfuse beside the hub

Langfuse is optional. With it, an operator sees each loop's spend, tokens,
turn times, tool mix and failures, but no content (ADR-0064). It runs as
the `langfuse` compose profile in `docker/langfuse/compose.yml`. It's off
by default, and the hub runs the same without it. The commands run from
the checkout as a user who can reach docker, on a hub that uses the docker
runtime.

- **Footprint:** about 2.6 GiB of RAM idle, whatever the traffic (1.8 GiB
  measured just after a start), and about 40 KB of disk a turn. That's six
  containers: web, worker, ClickHouse, Postgres, Redis and MinIO.
- **What it reaches:**
  - The datastores and the worker sit on an internal network with no
    route out.
  - The web container also joins the hub's egress network. There the
    egress proxy's relay reaches it as `http://langfuse-web`. It never
    joins a loop's network.
  - It also joins a bridge network that carries only the UI's port, on
    `127.0.0.1:3000`.
  - Langfuse's own telemetry is off, sign-up is off, and no model
    provider is set.

### Turning it on

The profile joins the hub's egress network, which the hub makes at its
first start with the docker runtime. Start the hub first, then:

```bash
bash scripts/langfuse-setup.sh --admin-email you@example.com
docker compose -f docker/langfuse/compose.yml \
  --env-file docker/langfuse/langfuse.env --profile langfuse up -d --wait
```

- **The setup script:** it writes two files, both 0600, ignored by git,
  and never overwritten:
  - `docker/langfuse/langfuse.env`, which compose reads. Every secret in
    it is generated: the datastores' passwords, the project's key pair,
    and the admin's password. Keep it as you'd keep `hub.key`, and keep
    it from the hub's user: every bare loop runs as that user.
  - `docker/langfuse/hub.env`, the key pair alone, which the hub reads.
    Anyone with the key pair can read every trace.
- **A hub with a non-default proxy image:** its egress network has the
  image's name. Pass it with `SPOOL_EGRESS_NETWORK=<name>` to the setup
  script, which writes it into the env file.
- **Signing in:** the UI is at `http://127.0.0.1:3000` on the hub's
  machine, through `ssh -L 3000:127.0.0.1:3000` from elsewhere. Sign in as
  the admin with the password from the env file.
- **Through Caddy:** add a site with `reverse_proxy 127.0.0.1:3000`, then
  add `LANGFUSE_URL=https://<that name>` to both files, run the `up`
  again, and restart the hub. The UI keeps its own login.
- **Telling the hub:** give it `hub.env` and restart it.
  - **Give the hub's user the file**, if you ran the setup script as
    someone else:
    `sudo chown spool docker/langfuse/hub.env`, keeping it 0600. A file
    the hub can't read doesn't stop it. It starts with whatever it had
    stored and warns `the hub's Langfuse file is not read`.
  - **At start the hub reads** `docker/langfuse/hub.env` in the
    checkout its binary sits in (`/opt/spool/bin/spool` reads
    `/opt/spool/docker/langfuse/hub.env`), or the file `--langfuse-env`
    names. Traces are then on, with the file's key pair and its
    `LANGFUSE_URL` if you set one (ADR-0064 item 3). The log says
    `langfuse settings from the hub's Langfuse file`.
  - **A file the hub can't use stops it.** That covers a line that isn't
    `NAME=VALUE`, a missing `LANGFUSE_PUBLIC_KEY` or
    `LANGFUSE_SECRET_KEY`, or a `LANGFUSE_URL` that isn't https, or http
    on this machine. The error names the file and what is wrong in it.
  - **While the file is there, it wins.** `PUT /api/settings/langfuse`
    refuses to change what it set. To change a setting, edit the file and
    restart the hub.
  - **A Langfuse set up before `hub.env` existed** has `langfuse.env`
    alone. Write the hub's file from it, then give it to the hub's user:
    ```bash
    (umask 077; grep -E '^LANGFUSE_(PUBLIC_KEY|SECRET_KEY|URL)=' \
      docker/langfuse/langfuse.env > docker/langfuse/hub.env)
    chmod 600 docker/langfuse/hub.env   # > keeps an existing file's mode
    ```

### Reading figures back

- **The hub reads each loop's figures back** from the read address,
  with the read key, every five minutes (#1300). With the profile,
  `hub.env` sets both. `GET /api/loops/{name}/figures?window=24h` serves
  a loop's turns, failures, spend, tokens, turn durations, tool mix and
  per-turn rows over `1h`, `24h`, `7d` or `30d`, numbers and names only.
- **Spend is the hub's own.** Langfuse holds no cost for Claude Code's
  spans, so the figures take spend from the turns the hub prices
  already, joined to a trace by its session. Tokens, durations, tools and
  failures come from Langfuse.
- **`langfuse` in the answer says what it holds:** `ok`; `stale`, the
  last figures while Langfuse isn't answering; `pending`, before the
  first read; `off`, no read address or read key; `unsupported`, a cloud
  loop, which exports nothing.
- **A restart reads the last 30 days again.** The hub keeps the figures
  in memory, not in `spool.db`.
- **A bare loop's figures are trusted as far as the loop**: it can post
  spans in another loop's name (ADR-0064 item 4).

### Turning it off

- **Moving `hub.env` away and restarting the hub** turns every loop's
  export off at its next session start. The hub clears the settings it
  took from the file and warns `the hub's Langfuse file is gone`,
  naming it. If you didn't mean to turn traces off, that line is the
  clue: the file moved, the binary runs from another checkout, or
  `--langfuse-env` is mistyped. Settings stored through the API, with no file,
  are turned off by clearing the upstream there.
- **With bare loops,** also rotate or delete the project's key pair in
  Langfuse's UI. A bare loop holds the key in its environment and may
  have kept a copy (ADR-0064 item 5).
- **Stopping Langfuse:**
  `docker compose -f docker/langfuse/compose.yml --env-file docker/langfuse/langfuse.env --profile langfuse down`
  stops it and keeps its data.
- **Removing it for good:** add `-v` to that command to delete its
  volumes, then delete `docker/langfuse/langfuse.env` and `hub.env` and
  restart the hub, which then turns export off. The hub's egress network
  is the hub's, so it stays.

### Backups and upgrades

- **Not backed up:** `spool-backup.sh` doesn't back up Langfuse's volumes.
  They hold telemetry, which builds up again once loops run, so losing
  them loses history, not the fleet.
- **Upgrading Langfuse:** the images are pinned by digest in the compose
  file, so it moves only with a Spool release that changes them. After
  pulling one, run the `up` again.
