---
title: Cloud loops
weight: 4
---

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
egress allowlist ([CONTAINMENT.md](https://github.com/enes-alatas/spool/blob/main/docs/CONTAINMENT.md)) as an environment
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
