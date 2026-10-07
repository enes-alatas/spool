# ADR-0042: The hub pins a PreToolUse hook that refuses the mechanical fleet rules

Date: 2026-10-03 · Status: accepted (operator decision of 2026-10-02, recorded on #529) · Amended: 2026-10-07 (items 1 and 4: the hook is handed the fleet's names, and refuses mentions, #628)

## Context

The fleet rules a loop works under live in its prompt (ADR-0024), and a
loop that slips breaks them anyway. Some of the rules are mechanical: a
command either is `git stash pop` or it is not. Several loops on one host
share a stash stack, a process table and a remote, so one slip of those
costs another loop its work. Review and CI catch slips after the fact, in
commits and UI strings (#472). Nothing stops one as it happens.

Claude Code runs settings hooks around each tool call. A PreToolUse hook
reads the call as JSON on stdin, and an exit status of 2 blocks the call
and hands the hook's stderr to the model as the reason. On 2026-10-02 the
operator chose these hooks for the rules (#529). Mods, which can do more,
are revisited when they leave preview and run headless.

A hook from `--settings` is not safe from the loop's own settings by
default. Probed on Claude Code 2.1.288 (#529): `"disableAllHooks": true` in
the workspace's `.claude/settings.json`, its `settings.local.json` or the
user's `settings.json` turns off every hook, the `--settings` ones
included. That is one line a hostile issue could ask a loop to write.
`--settings` outranks all three files, though, and a `disableAllHooks` of
false set there holds against each of them. A file cannot remove a hook
either: hooks from every source run together.

## Decision

1. **The hub passes the hook in `--settings` and pins `disableAllHooks`
   false.** The JSON is inline, since it holds no secret, and sits beside
   `--mcp-config` in `internal/claude`. This mirrors `--strict-mcp-config`:
   what the hub configures, the workspace cannot take away. Managed policy
   settings still outrank it; those are the operator's own machine policy.
2. **The hook is `spool-hook`, a small stdlib binary** (`cmd/spool-hook`,
   rules in `internal/hook`). `spool` takes flags, not subcommands, and a
   workstation should not carry the hub. A bare loop runs the one built next
   to the `spool` binary, or the `--hook-bin` path. A hub that has neither
   logs that its bare loops run without it. A docker loop runs
   `/usr/local/bin/spool-hook`, which the default workstation image carries.
   An operator's own image without it runs its loops unguarded: the hook
   command fails, and Claude Code lets the call through.
3. **The hook reads Bash calls and refuses only what is mechanical.** It
   splits the command into simple commands the way a shell would, through
   quotes, separators, substitutions and wrappers such as `sudo`, `env`,
   `timeout` and `sh -c`. A here-document's body is text, not commands,
   so a loop can write a PR body that names a refused command. It refuses
   with one line that names what to do instead. Input it cannot read is
   let through: a format change must not stop every tool call.
4. **The rules arrive one slice at a time under #529.** The first is the
   shared state of the host: a `git stash` that leaves or takes an
   unnamed entry, `git stash clear`, `pkill` and `killall`, a force push
   to or deletion of `main` or `master`, and `git add -A`, `--all`, `.` or
   `:/`. Mentions and signatures in `gh` bodies, credential shapes, and
   writes outside the loop's own workspace follow.

   **Amendment (2026-10-07, #628): the hook refuses mentions in `gh`
   bodies.** These are the operator decisions of 2026-10-03, recorded on
   #529.
   - **What is refused.** A `gh issue create|comment|edit`, a `gh pr
     create|comment|edit|review`, or a `gh api` field or input whose body
     `@`-mentions a fleet loop's name, its bot's username, or an allowed
     person's username. On GitHub such a mention pings whoever holds that
     login there. A name is matched as GitHub would read it: its case is
     ignored, and a name with an underscore stops there. A mention in a
     code span or a fenced block is let through, since GitHub renders it
     as text, and so is an email address.
   - **Bodies from a file, stdin, a shell variable or a substitution.**
     Such a body is read from the file, when it is there yet, and from the
     whole script besides. The script may write the file first, feed stdin
     from a here-document or a pipe, or set the variable a `--body "$body"`
     expands, so the check looks at the script as a whole. A command
     substitution, as in `--body "$(cat notes.md)"`, may read a file
     already there, so every file a word of the script names is read too.
   - **The hub hands the hook the names.** It passes them at each wake as
     `spool-hook --refuse-mentions <names>`, so the pinned `--settings` is
     built per wake rather than a constant. The pin itself is unchanged.
     The names are the loops', their bots' and the allowed people's: the
     same catalog the prompt teaches, which holds no secret.
   - **On by default, and the fleet can turn it off.** Any fleet whose
     loops share one GitHub identity has the same stray-ping problem. A
     fleet whose loops are their own GitHub users turns it off with
     `mention_guard` in `PUT /api/settings`. The change reaches each loop
     at its next wake.
   - **No signature check.** A signature line is a convention a fleet
     writes in its own rules, not something Spool enforces, so rule 1
     drops its signature clause.

## Consequences

- A slip is refused before it runs, and the loop reads why in the same
  turn, so it can correct course without anyone reviewing it.
- **The hook guards against slips, not adversaries.** A bare loop runs as
  the hub's user, and a docker loop has passwordless sudo, so a loop set
  on it can edit the hook or its settings. It can also put a refused
  command in a script and run the script. Containment is the
  workstation's job (ADR-0017, ADR-0028), not the hook's. The operator
  accepted this posture on 2026-10-03 (#529).
- Every Bash call spawns one short process. That is the cost of the
  guard.
- fakeclaude runs `--settings` hooks on a `!bash` directive and models
  `disableAllHooks` precedence as probed. That a PreToolUse exit of 2
  blocks the call and feeds stderr back was observed in one tier-3 call
  on 2026-10-03, which the fidelity ledger records.
