# ADR-0067: An operator's agent manages the hub through Spool's MCP server, with a scoped token

Date: 2026-11-08 · Status: accepted (the operator decided on 2026-11-07 that Spool is manageable by agents through an MCP server of its own, with a first cut in 1.0, #1400; he answered the four open questions on 2026-11-08, in a direct message to Milo, recorded on #1408: items 2 and 5 as written, item 4's operator listener, item 2's narrower resume and item 3's 90-day default; he accepts the whole on merge of #1408, per the 2026-11-08 thread there) · Relates to: ADR-0026, ADR-0030, ADR-0046, ADR-0048, ADR-0056, ADR-0061

## Context

An operator who wants their own agent to look after the hub, for
example to pause a loop or check on the fleet, can give it only the
operator token. That token is the hub's master key: it has no scope and
no end date (ADR-0048 item 6). An agent holding it in a cloud session
keeps it as a network secret on its environment. A prompt-injected
session, or a VM someone else can reach, then holds the whole hub
(#1282).

ADR-0061 removed this shape for loops. Each cloud loop has a token of
its own, scoped to its own `/mcp`, shown once, kept only as a hash and
revocable. The operator role has nothing like it.

The operator decided on 2026-11-07 that Spool should be manageable by
agents, through an MCP server of its own, and that a first cut belongs
in 1.0 (#1400). This ADR decides what that first cut lets an agent do,
the token it holds, and where it is served.

## Decision

1. **An agent token is a credential of its own, never the master key.**
   - An admin mints one under a name, under the loop-name rule and
     unique on the hub. The token belongs to the user who minted it,
     its *owner*.
   - It is 32 bytes from `crypto/rand`, written as `spool_agent_` and
     43 characters of unpadded base64url.
   - The hub keeps only its SHA-256, as it does for sessions
     (ADR-0048 item 4) and loop tokens (ADR-0061 item 4). A backup or a
     copy of the database holds no usable token.
   - **A rule matches its shape.** The redactor masks values the hub
     holds, and the hub holds only the hash, so it can't recognise a
     pasted token by its value. #1402 adds a pattern for that shape to
     `internal/redact` and to the secret-scan patterns, each with a
     self-test, so a token pasted into a message or left in a transcript
     is masked and a committed one fails CI.
   - The mint answer shows the token once, `Cache-Control: no-store`,
     and no log carries it. After that it is listed by name, owner,
     when it was made and when it was last used, never by value.
   - **The two credentials never stand in for each other.** The
     operator listener's API refuses an agent token on every route
     (401), and the agent's MCP server refuses the operator token, a
     user's session and every loop token. Tests hold both directions.

2. **Its scope is fixed: list the loops, pause one, resume one.**
   - The MCP server offers three tools and nothing else:
     - `list_loops`: each loop's name, runtime, state, whether it is
       paused, its health state (`ok`, `warning` or `failing`,
       ADR-0053) and when it last took a turn. It carries no problem's
       detail text, mission, message, secret or token, so no error
       string from a turn reaches the agent.
     - `pause_loop`: it takes a loop's name and does what the control
       room's Pause does.
     - `resume_loop`: it takes a loop's name and does what Resume does,
       within the bound the operator chooses below.
   - The scope is the server, not a check per route: a call the server
     has no tool for can't be made with the token. There is no scope to
     pick when minting, since 1.0 has one.
   - Pause and resume are on the loop's change record as any change is,
     naming the token as the actor by its name and its id
     (`agent:<name>#<id>`), so a token minted later under a revoked
     one's name is told apart. Reads are not recorded. A full per-call
     audit is out of scope (#1400).
   - **An agent resumes only what an agent paused** (operator decision
     of 2026-11-08, from the two options Elliot's security read on
     #1408 set out). A person may have paused a loop on purpose: a
     runaway spend, or a leak being contained. A resume would undo
     that, spend plan tokens and restart what was stopped.
     - A paused loop keeps who paused it until it is resumed: pausing a
       loop that is already paused, by an agent or a person, changes
       nothing and keeps the first pauser. So an agent can't re-pause a
       person's pause to make it its own (Elliot's re-check on #1408).
     - `resume_loop` works only on a loop whose current pause is an
       agent's, and refuses any other, saying so.
     - The worst a stolen token does is then pause loops: a denial of
       service the operator undoes and revoking the token ends. It
       reads no mission, message or secret, and changes no config.

3. **It is revoked at once, and ends with its owner.**
   - Its owner or an admin revokes it. The next call with it is refused
     (401), whichever listener it arrives on, and a session already open
     on the server is refused at its next call.
   - Removing a user revokes every agent token they own, as it signs
     out their Claude logins (ADR-0060's #1148 amendment).
   - **A token ends 90 days after it is minted** unless its owner picks
     another date or none at mint (operator decision of 2026-11-08). A
     network secret on a cloud environment lives as long as the
     environment does, and a default end bounds one nobody remembers
     better than a column someone has to read.
   - The list shows when each token was last used, written at most once
     a minute per token, so calls on a public listener don't each write
     to the database.
   - There is no rotate. Minting a new one and revoking the old one does
     the same, and rotation through the MCP itself is out (item 5).

4. **The server is served on the cloud listener, and on the operator
   listener for an agent on the same machine.**
   - The path is `/agent/mcp` on both, speaking the same streamable
     HTTP as the loops' `/mcp` (ADR-0026), stdlib only.
   - **On the cloud listener** it reaches an agent in a Claude Code
     cloud session, the case #1282 reports. It sits under ADR-0056
     item 4's bounds, which hold before any token is read. Like `/mcp`,
     it is there only while the operator has the cloud listener on.
     A wrong token costs one hash and one lookup, and 256 random bits
     can't be guessed, so no limiter is added; `/mcp` has none either.
   - **On the operator listener** it reaches an agent on the operator's
     own machine, such as Claude Code in a terminal, without opening a
     public address. It is under that listener's host and cross-site
     checks (ADR-0030), as every route there is.
   - **Never on the loop listener.** Workstations reach that one, and a
     loop's job is not to manage the hub.
   - A bare loop runs as the operator's own user, so it can read a
     token a local agent keeps in that user's files, as it can read the
     operator token in the data directory today (THREAT-MODEL.md: a
     bare loop is uncontained). This ADR doesn't change that.
   - A contained loop reaches the loop listener and its allowlist's
     hosts (ADR-0028), not the operator's files, and holds no agent
     token. A cloud loop reaches the cloud listener,
     but the token its proxy adds is a loop token, which `/agent/mcp`
     refuses (item 1).

5. **What stays out until after 1.0, and why.**
   - **Mission edits.** A mission is every turn's prompt. An agent that
     can rewrite it can make any loop do anything the loop can, which is
     the whole hub by another route.
   - **Rotating tokens**, a loop's or the operator's. A hijacked agent
     that rotates holds the hub and locks the operator out.
   - **Users and roles.** Only owners manage users, behind a password
     (ADR-0048's #690 and #923 amendments). A token can't type a
     password.
   - **Connections and secrets.** They are the credentials of
     everything the hub reaches.
   - **Messages, wake and kill.** A message is a prompt into a loop. Wake
     spends plan tokens, and kill revokes a cloud loop's token for good.
     Each widens what a stolen token does beyond a pause the operator
     undoes.

   Each later tool comes with its own scope at mint and an amendment to
   this ADR, with a security read.

6. **It ships in three slices, as children of #1400.**
   1. #1402, hub: mint, list and revoke agent tokens, and the refusal
      on the operator API.
   2. #1403, web: Settings mints a token, shows it once, lists and
      revokes them.
   3. #1404, hub: the MCP server and its three tools on both listeners.
      #1282 closes when it ships.

## Alternatives considered

- **Scoping the operator token.** It stays the owner's credential for
  the CLI and automation (ADR-0048 item 6), and scoping it would break
  them. A second credential leaves the master key as it is.
- **The agent calls the operator API with its token.** The scope would
  then be a list of routes checked on every request, and every new
  route would have to remember to refuse it. A server holding only the
  three tools can't be widened by forgetting a check.
- **Sealing the token under the hub key** (ADR-0046) instead of hashing
  it. Sealing keeps a value the hub can read back. The hub never needs
  to, so a hash is the smaller exposure.
- **The cloud listener alone.** A local agent would then need the cloud
  listener on and a public address to reach the hub it runs next to.
- **An agent resumes any paused loop.** It is simpler, but a stolen
  token could then undo a pause a person made on purpose, such as one
  stopping a runaway spend. The operator chose the narrower resume.
- **No end date unless one is set at mint**, as the operator token has
  none. A forgotten token on a cloud environment would then live as long
  as the environment. The operator chose a 90-day default.
- **A scope chosen at mint in 1.0.** With one scope there is nothing to
  choose. The field comes with the first tool that needs one.

## Consequences

- An operator's agent can watch the fleet and pause or resume a loop
  without the master key. A leaked agent token pauses loops until it is
  revoked or ends, and does nothing else.
- The hub has a third kind of bearer token, beside the operator token
  and loop tokens. Each listener's auth names which kinds it takes.
- The cloud listener serves one more path. Its "nothing else" rule
  (ADR-0056 item 4) gains `/agent/mcp`, which takes agent tokens only.
- THREAT-MODEL.md gains the agent token: what it reaches, and that a
  bare loop can read a local agent's copy.
- An agent can't edit a mission, rotate a token or touch users,
  connections or secrets until a later ADR scopes each.
