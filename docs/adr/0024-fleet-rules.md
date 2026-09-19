# ADR-0024: Fleet rules are injected ahead of every mission

Date: 2026-09-14 · Status: accepted · Amended: 2026-09-18 (wake cost; handoff turns)

## Context

Every operating rule a loop should follow — "sign your work", "one open PR at
a time", "never @mention on GitHub" — could only be set by pasting it into
each loop's mission. Shared conventions drifted between loops and had to be
re-edited in every mission as the fleet grew (#33). Two such rulings came out
of the 2026-08-20 dogfooding alone.

The open questions on #33 — placement and precedence, rule shape, a size
guard, and what counts as a prompt-contract change — were delegated by the
operator to the loops ("all sounds fine to me you guys decide with each
other", Telegram group, 2026-08-20) and settled on the issue. Role-scoped
rule sets are L5 Team territory and stay out of scope.

## Decision

1. **A rule is data in the DB** (ADR-0006): `id`, `title`, `body`, `enabled`,
   edited only through the API and the control room. Disabled rules stay
   stored — a disabled rule is a decision, not an absence.
2. **Enabled rules render as a `FLEET RULES` section ahead of `MISSION`,
   in creation order,** and the section ends with one explicit line: where a
   fleet rule and the mission conflict, the fleet rule wins. That is a chosen
   property, not a side effect of ordering — rules exist to constrain loops
   fleet-wide, so a mission must not be able to opt out, and an operator can
   override a loop's mission fleet-wide without editing it. Creation order is
   stable, so the section does not reshuffle between wakes and invalidate
   the prompt prefix of every loop at once.
3. **A change lands on each loop's next wake.** Prompts are built per wake,
   so nothing restarts and no running turn sees a mid-flight change.
4. **Three caps, enforced by the API and reported by it:** a title is at most
   120 characters, a body at most 1 200, and the rendered enabled section —
   header and conflict line included — at most 4 000 (about a thousand
   tokens, which a fleet of a hundred loops each pays every wake). The
   section cap is enforced on any write that grows the rendered section:
   create, edit, and enable. Rejections carry `code: rules_too_large` and the
   budget, and every read and write answers with the budget, so a client can
   show the wall before the operator hits it rather than a 400 after.
5. **The mechanism is the prompt contract; a rule's text is not.** The
   section existing, its placement, its rendering and the conflict line
   change as a `feat` with tier-2 fixtures (CONVENTIONS § Prompts). Editing
   a rule is data and churns no fixture.

## Consequences

- One rule, every loop: fleet conventions stop drifting, and a durable ruling
  from chat becomes a rule instead of a mission edit per loop (ADR-0023).
- Every loop pays the section on every wake; the caps keep that bounded, and
  the budget makes the cost visible where the operator is typing.
- A mission cannot override a rule. That is the point; a loop that needs an
  exception needs the rule changed, not its mission.
- Extending to role-scoped rules later means adding a scope to the rule and
  a role to the loop — the section, its placement and the guard stay as they
  are.
- Seed data is data: the two dogfooding rulings from 2026-08-20 are entered
  through the control room once this ships, not shipped in a migration.

**Amendment (2026-09-18, #162): what "lands on each loop's next wake" costs.**
Decision 3 was written as if building the prompt per wake were enough. It is
not: `claude --resume` keeps the system prompt its session was created with
and ignores a changed `--append-system-prompt`, so a rule saved between two
wakes reached the flag and never the loop. Every loop in the dogfooding fleet
ran without a rule the operator had saved and believed was binding, and two
tier-2 rows asserted the opposite — against a fake that honoured a changed
prompt on resume, which the CLI does not (ADR-0001).

Decision 3 now reads: **a change binds each loop from its next wake, and
reaches its system prompt at its next rotation.**

A wake that renders a prompt differing from the one its session was created
with delivers a `[system note · your standing instructions changed]` envelope
ahead of that wake's own envelopes. That is the promise decision 3 made: the
rule binds the loop on its very next turn. It lives in the transcript rather
than the system prompt until the session is replaced, and every fresh session
is spawned with the prompt rendered at that wake, so no separate mechanism is
needed to put it back where it belongs.

Detection is a hash of the whole prompt, so nothing that changes goes
unnoticed, and a burst of edits between two wakes costs one note rather than
one each. The note carries the three sections that change outside a release
— `MISSION`, `FLEET RULES` and the identity catalog — each whole, and says
plainly that the rest of the prompt may have moved too and is not repeated.
Two other renderings were tried first and are recorded here so they are not
retried: naming only the fleet's sections announced a change and then showed
a loop its unchanged rules while an edited mission went undelivered, the same
bug one field over; replaying the prompt verbatim fixed that and put the
prompt's own worked example of an envelope header — a plausible `ref:42` — in
the transcript in a position that reads like an arriving message, which is
the invented reference ADR-0025 exists to prevent.

**Rejected: rotating on a prompt change.** The obvious completion — arm a
rotation under ADR-0022 whenever the prompt differs, so the system prompt
catches up on its own — was built and measured, and costs more than it buys
(operator ruling, 2026-09-18, recorded on #162). Every fleet change is a
prompt change for every loop: adding a loop changes each existing loop's
catalog, so one creation would rotate the whole fleet, a handoff turn each.
It buys nothing for correctness, since the note has already bound the loop.
An operator who wants a loop's system prompt current sooner than its next
rotation has rotate-on-demand (#93), which spends that turn deliberately and
for one loop.

The note's existence, its header and the sections it carries are prompt
contract under decision 5.

**Amendment (2026-09-18, #154): a handoff turn is not told.** Decision 3, as
amended above, promises the note "ahead of that wake's own envelopes … on its
very next turn". One turn is an exception: the handoff turn of a rotation.
That session is ending, its reply is a note to its successor, and standing
instructions it can no longer act on would only crowd that out.

Nothing is lost by it. The successor is spawned with the prompt rendered at
that wake, so the rule binds it by the stronger of the two mechanisms — its
system prompt, not a line in a transcript. What the exception costs is the
*telling*: a fresh session adopts its prompt as its baseline (`promptAdopt`),
so no note is delivered on that path and the successor is bound without being
shown what changed. That is accepted: a session reading its own system prompt
needs no announcement of what it already contains.

The exception is narrow and deliberate — it applies to the handoff turn only,
not to any other turn of a rotating loop, and a note owed to a session that
dies before its handoff is still owed, because nothing marks it paid until a
turn completes.
