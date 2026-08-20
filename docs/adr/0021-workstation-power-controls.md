# ADR-0021: The operator can power a workstation, and switched-off is not down

Date: 2026-08-20 · Status: accepted

## Context

ADR-0017 made a workstation long-lived: it survives sleeps, pauses and
orchestrator restarts, and "dies only with the loop". That is right as a
statement about the *engine* — nothing in the loop lifecycle may take a
workstation away — but it left the operator with no way to act on one. A
wedged container, a stale image, a machine that wants rebuilding from scratch:
the only tool was deleting the loop, which throws away its mission, history and
schedule to fix its machine (#13).

Two things make this more than adding four endpoints. The runtime seam already
had a method called `PowerOff`, and it *destroys* — `rm --force` plus
`volume rm --force`. And a workstation that has been switched off is, to a
health poll, indistinguishable from one that has crashed: both are simply not
reachable. Rendered naively, a successful power-off shows up as the alert
`workstation_down`, i.e. a control whose success reads as a failure.

## Decision

1. **Four operator-facing verbs**, each `POST /api/loops/{name}/workstation/…`
   with an empty body, answering with the loop as it stands afterwards:
   `restart` (halt + ensure), `poweroff` (halt, keeping the volume),
   `poweron` (ensure), `recreate` (destroy + ensure). `recreate` is its own
   verb, never a flag on `restart`, because it is the destructive one.
2. **The seam gets honest names.** `Runtime.PowerOff` becomes `Destroy`, what
   it always was; `Halt` is new and stops a workstation without destroying it;
   `HasWorkstation` reports whether a runtime has a machine of its own to act
   on. The bare runtime returns false — the host is the workstation — so all
   four verbs answer `409` there, and the control room hides the controls.
3. **Intent is recorded only once the verb has happened.** A failed verb
   leaves everything as it found it: a halt that fails must not suspend a
   loop whose workstation is still up, and a power-on that fails must not
   discard an off-intent that still holds. The cost of ordering it this way
   is that a crash between halting and recording loses the intent — a
   self-healing loss, since the next wake brings the workstation up, which is
   what the operator would then see anyway.
4. **Power-off is intent, and intent is persisted.** A poll cannot observe why
   something is unreachable, so `loops.workstation_off` records the operator's
   switch. A workstation switched off yesterday still reads "powered off"
   today rather than "died overnight". Only `poweron`, `restart` and
   `recreate` clear it.
5. **`workstation_off` is a state of its own**, calm, and distinct from the
   `workstation_down` alert. Precedence is `workstation_down` > `paused` >
   `workstation_off`: an alert always wins; pause is a statement about the
   loop, which outranks a statement about its machine. The wire carries
   `down_reason` (`""` | `powered_off` | `unreachable`) on the loop view and
   on a new `workstation` SSE frame, so a panel about the machine can read the
   fact directly instead of decoding a state with precedence rules.
6. **A switched-off workstation stays off.** While the intent stands, ticks are
   skipped — not queued, so power-on is not met by a backlog of stale wakes —
   and inbound messages go to the loop's stored inbox without waking it. They
   are delivered when the machine comes back, the same path pause uses.
7. **Power and pause stay independent switches.** Power-off does not pause;
   power-on does not resume. Each restores exactly what it turned off, so
   powering a machine back on cannot silently un-pause a loop the operator
   paused for unrelated reasons. Both can be set at once.
8. **Every verb that takes the workstation away ends the running turn first.**
   The claude process is killed and its exit recorded before the workstation
   is touched, so a turn is never silently truncated. `poweron` is exempt
   because it is documented idempotent: called against a workstation that is
   already up it must change nothing, least of all eat a turn.
9. **The calls are synchronous.** They wait on a container lifecycle operation
   and answer with the resulting state, so the control room renders a result
   rather than a promise it must later correct. QUALITY.md exempts these four
   endpoints, by name, from the p95 < 100 ms API baseline. A `workstation`
   frame goes out when a verb *starts*, carrying the verb, so the control
   room can show work in progress for a recreate that runs longer than the
   request's connection.

## Consequences

- **`recreate` destroys the loop's memory of the conversation.** The volume
  holds `~/.claude`, so the session cannot be resumed: Spool forgets the
  session id and the next wake starts a fresh one. Spool's own record — every
  message, turn and event — survives untouched. That asymmetry is the sharpest
  edge for the operator, so the control room quotes it in the confirm dialog,
  along with the rest of what goes: uncommitted work in the workstation, and
  anything the loop installed. The loop row, mission, schedule, secrets and
  injected credentials all survive.
- **This amends ADR-0017's "dies only with the loop"** — deliberately, and
  only for operator action. No engine path (sleep, pause, restart, boot
  recovery) may halt or destroy a workstation.
- **A loop can be off without being paused**, which is a new combination for
  anything that reasons about loop state. The precedence rule is what keeps
  the fleet view legible; a panel about the workstation should read
  `down_reason` instead.
- **The verbs are only as safe as the actor's serialization.** They run as
  commands on the loop's actor, so they cannot interleave with a wake — but
  they do block that actor for their duration, which for a cold `recreate` can
  be minutes. Acceptable: the loop has no machine to run on meanwhile anyway.
- **Bare loops have no power story at all.** That is the honest answer rather
  than a simulated one: stopping the operator's host is not Spool's to do.
