# v0.6.0 release notes — VERIFIED DRAFT (Milo, 2026-10-26)

Base: v0.5.1 (2026-10-09). 325 commits in v0.5.1..v0.6.0 (tag on 54a1da4, 2026-10-26). Every bullet
names the commit subject(s) it rests on; a claim with no subject is not made.
NOT shipped, do not claim: ADR-0060 several Claude logins (docs only, #926
slices wait for after the tag); hardened cloud mode (#975, documented only).
Re-verify the "as of" commit on the day of the tag: `git log v0.5.1..<tag>`.

## Cloud loops (ADR-0056, ADR-0059)
A loop can run in a Claude Code cloud session beside contained loops.
- Create a cloud loop from New loop; it wakes in a cloud session driven
  through the `claude` CLI, the only path. [feat(store): a loop's runtime can
  be cloud; feat(spool): cloud loops wake in cloud sessions; feat(runtime):
  cloud loops run only through the claude CLI]
- Settings → Cloud: a guided setup with the cloud listener switch, the public
  address check, the Claude account sign-in with the CLI's own login, and the
  shared environment picked by name with the allowlist to paste and a confirm.
  [feat(web): Settings → Cloud, and a guided setup…; feat(httpapi): the cloud
  listener's switch and the public address check; feat(httpapi): sign in to
  the cloud account with the CLI's own login; feat(httpapi): Settings → Cloud
  picks the environment…; feat(web): Settings → Cloud walks the shared
  environment step]
- Each cloud session gets a hub token of its own, revoked at a stop; a stopped
  loop's page says a turn may still be running and links the session.
  [feat(loop): each cloud session gets a hub token of its own…; feat(runtime):
  a cloud loop says a turn may still be running after a stop; feat(web): a
  stopped cloud loop's page…]
- A cloud session is handed its loop's env-var connections as it starts; the
  room says when a connection change reaches a cloud loop, and the delete
  confirm names the secrets a session keeps. [feat(runtime): a cloud session
  is handed its loop's connections…; feat(web): a connection's page says when
  a change reaches a cloud loop; feat(web): deleting a cloud loop names the
  secrets…; feat(web): a cloud loop running without its secrets says so]
- A cloud turn's cost is the hub's estimate, shown as such or unknown.
  [feat(runtime): a cloud turn's result carries the hub's cost estimate;
  feat(store)/feat(web): …estimate, or unknown]
- A held cloud loop reads "needs setup"; an unconfirmed allowlist warns.
  [feat(web): a held cloud loop reads "needs setup"; feat(web): a cloud loop
  warns while its allowlist isn't confirmed]

## One Claude login through the Claude proxy (ADR-0058)
- Contained loops reach the model through the hub's Claude proxy on one
  claude.ai login, signed in from first run or Settings; it supersedes the
  setup-token and refreshes before it expires; Sign out revokes it. A hub with
  no claude of its own signs in with the workstation image's. [feat(httpapi):
  contained loops reach the model through the Claude proxy; feat(web): one
  Claude login…; feat(httpapi): a Claude login supersedes the setup-token;
  feat(loop): refresh the Claude login before its token expires; feat(httpapi):
  Sign out asks the source to revoke…; feat(cmd): a hub with no claude signs
  in with the workstation image's claude]

## Users, roles and sign-in (ADR-0048, 0050, 0051)
- The control room has users with passwords and sessions; the first start
  creates the admin; roles bound what a user may call and see. [feat(users):
  the hub keeps users…; feat(cmd): the first start creates admin, and spool
  user manages users; feat(httpapi): roles bound what a user may call…;
  feat(web): users sign in with a username and password…]
- Users link their Telegram or Slack account; a link code is the one way a
  person becomes an allowed sender; an owner sees links, blocks users, and
  manages users from Settings. [feat(web): a user links their Telegram or
  Slack account…; feat(route): a link code is the one way…; feat(httpapi): an
  owner blocks a user…; feat(web): an owner manages the hub's users…]
- A loop's owner is chosen as a user, and the room shows who created and
  changed a loop. [feat(httpapi): a loop's owner is chosen as a user…;
  feat(httpapi): the hub records who changed a loop; feat(web): the room shows
  who created and changed a loop]
- The first-run page can be finished without a chat surface.

## Private conversations (ADR-0049)
- Every allowed sender holds a private conversation (`dm:<name>`); a loop's
  page shows the conversations the reader may read; a linked user reads the
  ones that are theirs. [feat(route): every allowed sender holds a private
  conversation…; feat(web): a loop's page shows the private conversations…;
  feat(httpapi): a linked user reads the private conversations that are theirs]

## Corrections (ADR-0055)
- An edited or deleted Telegram or Slack message reaches its loops as a
  correction; a waiting message is corrected in place; the room shows the edit
  beside the message. [feat(telegram)/feat(slack): an edited … reaches its
  loops as a correction; feat(loop): a correction of a message still
  waiting…; feat(web): a person's edit or deletion shows beside…]

## Loop health (ADR-0053)
- The control room shows a loop's open problems; the owner is told once when a
  loop keeps failing or its workstation stays down; the Fleet row and loop page
  show what a loop is doing (`[doing: …]`). [feat(web): the control room shows
  a loop's open problems; feat(loop): the hub tells a loop's owner once…;
  feat(loop): a loop says what it is working on…; feat(web): the Fleet row and
  the loop page show what a loop is doing]

## Connections
- A `github-app` connection: the hub holds the app's key and mints hour-long
  installation tokens; a loop's git and gh fetch them through a helper, never
  the key. [feat(httpapi): a github-app connection mints…; feat(loop): a loop
  holding a github-app gets its token in git and gh; feat(web): the
  Connections page creates a GitHub App connection; feat(release): the archive
  and spool upgrade carry spool-github]

## Loop controls
- A loop can be set to work alone (Agent tool denied). [feat(loop)/feat(web)]
- A per-loop daily USD cap sleeps the loop until the day turns. [feat(loop):
  a loop at its own daily USD cap…; feat(web): a loop's page sets its daily
  spending cap]
- Plan usage only rises within a window, its reset fixed. [fix(loop)]

## Install, upgrade, backup (ADR-0052, ADR-0057)
- A tag builds and attaches the spool binaries with checksums and
  attestations, and publishes the images by digest; the hub never pulls, and
  `spool images` says how to fetch a release's images. [feat(release) ×2;
  feat(docker): the hub never pulls…]
- `spool upgrade` upgrades the hub a unit runs, with backup and rollback;
  `spool migrations` lists a build's migrations. [feat(cmd) ×2; fix(cmd): the
  upgrade backs up the data directory alone…]
- Backup and restore scripts for the data directory and workstation volumes.
  [feat(scripts); fix(scripts) ×4]

## Security hardening
- Each workstation gets its own network and no NET_RAW; the hub warns at boot
  on an engine that lets DNS past the wall. [fix(docker) ×2]
- The egress allowlist refuses an IPv4 literal and a one-label domain form, and
  reads `*.example.com` as `.example.com`. [fix(egress) ×2; feat(egress)]
- spool-hook refuses a gh or git call that carries a credential, and kills that
  reach other loops' processes. [feat(hook) ×2]
- The operator listener bounds what a caller can make it hold; the loop
  listener closes idle connections; members read no connection config or
  pairing code. [fix(httpapi) ×4]
- Toolchain go1.26.10 for GO-2026-6809. [fix]

## Control room polish
- Settings → Egress as a dialog with a filtered list; copy chips for literals
  an operator types; round loop controls; the mission edited in a dialog;
  web addresses are links; a Fleet row shows its loop's mission. [feat(web) ×6]


## Fixes between the first draft (9a4a484) and the tag, all in v0.6.0
Cloud loops, found by the first real cloud loop on 2026-10-23/24:
- A cloud start uploads the loop home whatever the CLI's flags say; takes its
  session's id in the VM's cse_ form; waits on through a CLI that exits
  cleanly; a failed start says why in the hub's log. [fix(runtime) ×4]
- A cloud session that can't reach the hub turns Settings → Cloud step 2 red,
  and the step shows a private-route pass and the session stage.
  [fix(httpapi); feat(web)]
- Pause and kill answer once a cloud loop is cut off; a stop that could not
  revoke a cloud token says so; the loop page's controls show the hub's answer
  when one fails. [fix(loop); fix(httpapi); fix(web)]
- Cloud-loop copy says what the operations guide says. [fix(web)]
Users and conversations:
- A private conversation names the Spool user it is with, and a member's
  private chat is named by their Spool user on the loop page. [fix(httpapi);
  fix(web)]
Hub and runtimes:
- A turn the claude process ended says why; an exit is read once its stderr
  has ended; every stderr excerpt starts on a whole line, so redaction holds.
  [fix(loop); fix(claude) ×2]
- No caller waits on a loop's actor once it has stopped; a tick asked for
  after the start is not jittered; a mirror subscribes before its surface's
  Start returns. [fix(loop); fix(sched); fix(surface)]
- Docker workstations: a burst of wakes asks docker each thing once, boot
  recovery reaps its loops side by side, a reap execs only where a claude was
  running, and the exec that starts claude writes its MCP config; the perf
  smoke now runs on docker too. [fix(docker) ×3; fix(loop); test(itest)]
- An account check doesn't record over a later sign-in or Remove.
  [fix(httpapi)]
- Upgrade keeps the previous images by name so the rollback can retag them;
  backup keeps the volume stream out of docker's container log.
  [fix(upgrade); fix(backup)]
README: the demo video shows the room as it ships, and a "Where loops run"
section shows a cloud loop running. [docs(readme) ×3]

## Decided 2026-10-26 (Enes, ref:6988; Elliot, #860 comment 6417835749)
- Number and headline: v0.6.0, "Cloud loops, users, and one Claude login".
- Upgrading from v0.5.1 (verified: cmd/spool/user.go has `reset`; OPERATIONS.md:112):
  - The setup-token keeps working after the upgrade until an admin signs in
    under Settings → Claude login; that sign-in replaces it. Cloud loops need
    the sign-in.
  - No re-linking: senders allowed before the upgrade keep working and keep
    their loops. A person links in Settings → Account only before they are
    chosen as a loop's owner.
  - By hand, this once: v0.5.1 has no `spool upgrade`, so follow
    OPERATIONS.md → Upgrading. The new admin's one-time password goes to
    stdout, which the unit discards: run `spool user reset admin`. A stored
    egress host that is now refused is removed in Settings → Egress.
