# ADR-0024: Fleet rules are injected ahead of every mission

Date: 2026-09-14 · Status: accepted

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
