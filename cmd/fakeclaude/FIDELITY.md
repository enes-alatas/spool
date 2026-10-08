# fakeclaude fidelity ledger

Tier 2 is only as strict as this fake. A fake more permissive than the real
CLI turns a green row into a test of our own assumptions: #162 had two rows
green on a system prompt the real CLI never re-reads on `--resume`. This file
lists every CLI behaviour `fakeclaude` models, and for each one how it was
checked against a real `claude`, or that it wasn't (ADR-0009, amended by #168).

**Verified** means a real CLI was observed doing it: the build, the date, and
where the observation is recorded. **Partly** means some of the modelled
behaviour was observed and the rest was not; the line says which part.
**Assumed** means nothing records the real CLI doing it — the fake's behaviour
is our guess. Measurements from probes live in ADR-0001 "Recorded CLI
behaviour"; this file points at them rather than restating them. A build here
is the one the observation ran on, which is not always
`internal/claude.TestedVersion` (2.1.281).

The rules, in `docs/QUALITY.md` "Review (human)":

- A PR that adds or changes a behaviour of the fake adds or updates its line.
- A PR that changes engine behaviour resting on an **assumed** or **partly**
  line runs the tier-3 check first, and records it here. That spends the
  operator's plan tokens, so ask first.

## Process and stream-json framing

| Behaviour | Status | Evidence |
|---|---|---|
| Silence until the first stdin user message, then `system`/`init` carrying `session_id`, `cwd`, `model` | partly | init carrying `model`: 2.1.282 and 2.1.281, 2026-09-24 (ADR-0033). Silence before the first stdin message: part of the stream-json contract the MVP's M1–M6 runs were built on (2026-08-15/16, when 2.1.233 was `TestedVersion`), never probed on its own. |
| A family alias is reported resolved in init (`opus` → `claude-opus-5-5`, …) | verified | 2.1.282 host and 2.1.281 workstation image, 2026-09-24/25, by the alias-resolution run (ADR-0033, #332). |
| Per turn: `assistant` event(s), then one `result` (`subtype`, `is_error`, `total_cost_usd`, `duration_ms`, `num_turns`, `result`, `usage`, `session_id`) | partly | The fleet's real turns on 2.1.281 parse through the same reader every day; no single probe records the full field list. |
| `--include-partial-messages` emits `stream_event` → `content_block_delta` → `text_delta` | partly | Token streaming worked in the MVP's M1–M6 runs (2026-08-15/16, when 2.1.233 was `TestedVersion`); the exact event shape is not recorded. |
| stdin EOF after a result: clean exit 0 | verified | `internal/claude/stream.go`'s own "(verified: …)" on CloseStdin, from the MVP (2026-08-15/16), naming no build: 2.1.233 was `TestedVersion` then. Exercised since by `make e2e-m1`'s idle-reap. |
| `--version` prints a version line | deliberate difference | The real CLI prints `X (Claude Code)`; the fake prints `2.1.281 (fakeclaude)`, so a log line tells them apart. |
| `--effort`, `--add-dir`, `--permission-mode` are consumed and ignored | assumed | Not measured on the real CLI (ADR-0001). Ignoring them is permissive in exactly the direction that would hide a pin. |
| Any other flag it does not parse is ignored, value and all | assumed | Production relies on this for `-p`, `--verbose`, `--strict-mcp-config` (`internal/claude/args.go`) and whatever a model's extra args add. Nothing records how the real CLI answers a flag it does not know, or what `--strict-mcp-config` does. So a misspelled or retired flag in `args.go` stays green in tier 2 whatever the real CLI would do. |
| A `rate_limit_event` whose `rate_limit_info.unifiedWindows` carries `five_hour` and `seven_day`, each a `utilization` fraction and a `resetsAt` in unix seconds, emitted before the turn's `assistant` events (`!usage`) | assumed | The shape is read from the 2.1.292 binary's own stream schema, 2026-10-08 (#647): emitted "when rate limit info changes", `unifiedWindows` marked internal and documented as tracking both windows on every observation. No run was watched emitting one, so neither its presence on stdout nor its place in the turn is recorded. |

## Sessions and resume

| Behaviour | Status | Evidence |
|---|---|---|
| `--session-id <id>` creates a session under that id, reported in init. The fake takes any string and never checks whether the id is already in use | partly | Creation with a fresh v4 uuid runs on the real fleet at every rotation (2.1.281). Not recorded: the real CLI's answer to an id that is not a uuid, or to one already in use. `internal/claude` and `internal/loop` say the CLI requires a uuid, and neither cites an observation. |
| `--resume` of an unknown session: exit 1, stderr `No conversation found with session ID: <id>`, no stdout | verified | The only record is `internal/claude/exit.go`'s own "(verified: …)" on IsSessionNotFound, from the MVP (2026-08-15/16). It names no build: 2.1.233 was `TestedVersion` then, not a build recorded as measured. Not re-measured since. |
| A resumed session keeps the system prompt it was created with; a new `--append-system-prompt` on `--resume` is ignored (`!sysprompt` reads it back) | verified | 2.1.276, 2026-09-18, the ADR-0001 probe (#162); seen first on a live loop. |
| `--model` and `--mcp-config` are read per spawn, resumed or not | verified | 2.1.276, 2026-09-18, the ADR-0001 probe. The MCP half compared the advertised server set, not a live connection. |
| `total_cost_usd` is the session's running total, repeated unchanged by a turn that bills nothing | verified | Live store rows from the 2.1.281-era fleet, 2026-09-19 (#191, fixed in Spool by #194). |
| Each `assistant` event carries its step's usage; the `result` sums usage over the turn's API steps (`!steps`) | partly | The summed result: a 761s tick turn reported 15.1M input on a ~200k window, 2.1.273, 2026-09-16 (#94, ADR-0022). Per-step usage on assistant events is inferred, not probed. |

## Failures the engine matches

| Behaviour | Status | Evidence |
|---|---|---|
| Over-window prompt (`!toolong`): errored result, `Prompt is too long`, zero usage, clean exit, session kept | partly | 2.1.238 with haiku-4-5, 2026-08-21, `make e2e-context` (#47). Recorded: `is_error`, zero usage, the text, the clean exit and the surviving session. Not recorded: the `subtype` — the fake's `error_during_execution` is assumed. |
| Unknown model (`--model` containing `nosuch`): errored result with `api_error_status` 404, `terminal_reason` `api_error`, the sentence naming the model, nothing billed | partly | 2.1.282 against a stub API, 2026-09-23/24 (#289). Recorded: `is_error`, 404, `total_cost_usd` 0 on a fresh session, the sentence, and `unrecognized_model` on stderr, which the fake does not write. Assumed: `subtype` `success`, the session surviving to resume on another model, and the repeated running total on a resumed session. |
| Rejected login (`login-expired` in `$FAKECLAUDE_STATE`): an assistant message with `"error":"authentication_failed"`, `model` `<synthetic>` and the CLI's sentence, then an errored result with `subtype` `success`, `terminal_reason` `api_error`, no `api_error_status` and zero usage | partly | 2.1.283, 2026-09-27: the fleet's own store kept every event of the turns that ran while the operator's login was expired (#405). Recorded: every field the fake emits and the sentence. The real CLI then exited 1 when the idle drain closed its stdin, about 90s later; the fake exits as it does after any turn. The engine reads no exit code on a drained exit, so nothing tier 2 asserts rests on that. Not recorded: stderr, and a revoked token rather than an expired one. |
| Spool MCP server failed (`mcp-failed` in `$FAKECLAUDE_STATE`): the init event lists `{"name":"spool","status":"failed","source":"dynamic"}` in `mcp_servers`, and the turn runs on without the server's tools, to an ordinary result | partly | 2.1.281, 2026-09-29: the fleet's store kept three such inits on one docker loop (#476), each followed by a `requesting` status and a turn that ran to `success`. Recorded: the entry's fields and the turn running on. Not recorded: why it failed, or the other statuses the CLI may report. Without the file the fake lists `spool` as connected whenever it has an MCP config, without dialing it. Every other server in a `--mcp-config` is listed as connected too, never dialed or run; how the real CLI reports an attached server it can't reach isn't recorded (#597). |
| Mid-turn session loss (`!lost`): exit 1 with the unknown-session stderr line | assumed | Reuses the resume-time signature above; a session lost mid-turn has never been observed. |
| Crash mid-turn (`!crash`): exit 2, no result | assumed | The real CLI's exit code on a crash is not recorded. The engine classifies any non-zero exit without a result, so the code itself carries nothing. |
| An unresumable session (`.fakeclaude-resume-broken`): exit 1 before init, a diagnostic on stderr, no stream-json | assumed | The stderr line is invented. #47 set out to find a stderr signature and found none, which is why the engine classifies "exited non-zero before init" and never reads the text (#59). `make e2e-context` then showed an over-full context is not what causes this failure. A marker whose contents are `all` fails fresh spawns the same way. That is a test instrument, a loop that cannot run at all, and not a CLI claim. |

## Hooks

| Behaviour | Status | Evidence |
|---|---|---|
| `--settings` (inline JSON or a path) is read, and its hooks run | verified | 2.1.288, 2026-10-03: SessionStart and UserPromptSubmit hooks from `--settings` fired (probe on #529). The fake runs only PreToolUse, on `!bash`. |
| `disableAllHooks: true` in the workspace's `settings.json` or `settings.local.json`, or the user's `settings.json`, turns off every hook, `--settings` ones included, unless `--settings` sets it false | verified | 2.1.288, 2026-10-03, nine-case probe recorded on #529. |
| A PreToolUse hook that exits 2 blocks the call, and its stderr reaches the model as the reason; any other exit lets the call run | partly | 2.1.288 with haiku-4-5, 2026-10-03, one tier-3 call on #564 with `spool-hook` as the `--settings` hook. Recorded: `echo` ran (exit 0); `git stash pop` was blocked, the stash left in place, and the tool result was an error reading `PreToolUse:Bash hook error: [<hook command>]: ` followed by the hook's stderr. The fake replies `blocked: ` and the stderr instead. Not recorded: an exit other than 0 or 2. |
| The PreToolUse input carries `session_id`, `cwd`, `hook_event_name`, `tool_name` and `tool_input.command` for a Bash call | partly | `internal/hook` reads only `tool_name` and `tool_input`, and refused the right command in the tier-3 call above, so those two are recorded. The other fields are the documented contract. |

## Directives that are test instruments

These drive the engine from outside and make no claim about the CLI beyond
the row they rest on.

| Directive | What it stands for |
|---|---|
| `!echo`, the unscripted default | A model repeating its input, so a test can read what Spool put in the turn. |
| `!huge <bytes>` | A very long stdout line, to test Spool's reader. The real CLI's largest line is not measured. |
| `!ctx <tokens>` | A filling context: the input tokens each API step reports. The real CLI compacts on its own near a full window (2.1.238, 2026-08-21, `make e2e-context`), which the fake does not model. |
| `!usage <five-hour> <seven-day>` | The plan's usage changing during a turn, as a rate-limit event (its row under Process and stream-json framing is assumed). |
| `!hang <seconds>`, `nosuch-slow` | A slow turn. |
| `!env NAME` | A loop echoing an injected credential, which is what redaction has to catch (#150). |
| `!get URL` | A loop's outbound request, through Go's proxy-honouring client (#193). That the real CLI honours `HTTP_PROXY`/`HTTPS_PROXY` is **assumed**: ADR-0028 states it, and nothing records a measurement. |
| `!bash <command>` | The model calling the Bash tool. The PreToolUse hooks run on it (see Hooks above); the command itself never runs, and the reply is `blocked: <stderr>` or `ran: <command>`. |
| `!send {json}` | The model calling the hub's `send_message` tool mid-turn (ADR-0026). The call shape is the MCP SDK's and runs daily on the real fleet. Assumed: the inline-JSON `--mcp-config` form (production passes a path) and one connection per session, to the server named `spool`. |
