# Loop-prompt eval

The loop prompt (`internal/loop/prompt.go`) is a harness with no eval: every
change to it has been judged by reading it. This directory is the start of
one (#450). Each case is one turn, holding what the loop was shown and what
it did. The graders check that turn against the fleet rules that can be
checked mechanically.

## What is here

| Path | What |
|---|---|
| `synthetic/` | hand-written cases, one per known slip plus one clean turn. Checked in. A credential case lives in `internal/eval/eval_test.go` instead, because the PR secret scan keeps credential shapes in test files. |
| `signing.json` | which GitHub artifacts each loop's mission says to sign |
| `cases/` | cases exported from a live store, and `cases/prompts/`, each loop's system prompt as the hub builds it now. **Gitignored, never committed** (below). |
| `replays/` | cases a model's replay produced. Gitignored too: a replay quotes the case it replays. |

The code lives in `internal/eval` (cases, graders, report) and
`cmd/spool-eval` (export, replay and grade).

## Running it

```
go build -o bin/spool-eval ./cmd/spool-eval
bin/spool-eval export -loops terra,iris,milo,quinn   # into eval/cases/
bin/spool-eval grade eval/cases                      # offline graders
bin/spool-eval grade -online eval/cases eval/synthetic   # also resolves every sent link
```

Run from the repo root. The tool reads `scripts/secret-rules.awk` and
`eval/signing.json` by relative path.

`export` reads a snapshot of the store (`VACUUM INTO` a temporary file), never
the live database, so it is safe while the hub is running, and this
binary's migrations never touch the hub's file. By default it keeps the 15
most recent finished turns per loop from the last 7 days (`-per-loop`,
`-since`).

- **A hub's store is sealed under its hub key** (ADR-0046). The snapshot is
  opened with the live key through a link, never a copy; `-key` names it
  when it is not `hub.key` beside `-db`.
- **The redactor masks what the hub's does:** every secret in the store,
  the hub key as bytes and as hex, and the operator token (`-operator-token`,
  default beside `-db`; it is read, never minted).
- **The snapshot is a full copy of the store**, sealed secrets included. It
  is removed when the export ends or is interrupted. After a kill, sweep
  it: `rm -rf "${TMPDIR:-/tmp}"/spool-eval-*`.

## Why exported cases stay out of git

The repo is public. A redacted transcript is still the fleet's group chat,
its task discussion and its tool output, and anything committed stays
fetchable by SHA even after it is deleted. So the exporter and graders are
checked in, and the cases are regenerated locally. A verdict posted from a
run quotes pass rates and one-line grader details, never case text.

What export removes or withholds:
- **Every turn from a private conversation** is left out whole. Only a
  channel turn or a tick is exported; an owner DM, the control room, a
  `dm:<name>` conversation, and any kind added later are not.
- **A send anywhere but a channel** in an exported turn keeps its
  destination, but not its text.
- **Every secret Spool holds** is replaced by value, using the same
  `internal/redact` redactor the store uses.
- **Every credential shape and fleet identifier** in
  `scripts/secret-rules.awk` is replaced as well, which catches a value
  Spool never held.
- **Unfinished turns** (killed, or cut off by a restart) are skipped, because
  there is no reply to grade.

A status note (a turn's final reply) can still paraphrase a private
conversation. That is one more reason the cases stay local.

## The graders

| Grader | Rule | Applies to |
|---|---|---|
| `trailer` | a self-paced reply ends with `[next-wake:]` in range; a rotation handoff carries none | every case |
| `github_no_mention` | no `@name` in anything published to GitHub | writes whose text is visible |
| `github_signature` | the artifacts a loop's mission says to sign are signed | per `signing.json` |
| `commit_no_closing_keyword` | no `Closes #n` in a commit message; only a PR body closes (#412) | commits |
| `no_credential` | no credential shape in a send, command or reply; fleet identifiers not on GitHub | every case |
| `refs_shown` | every `reply_to`/`resends` ref was shown in the session or returned by an earlier send | sends with a ref |
| `group_reaches_someone` | a new channel message @mentions someone it reaches: a loop in that channel, a person, or `@all` | new channel sends |
| `doing_fits` | a `[doing:]` line is at most 60 characters, the most the store keeps | replies with one |
| `links_resolve` | every link in a send resolves, anchor included (#419); `-online` only | sends with a link |

The graders read what a write *publishes*, meaning a commit message or an
issue, PR, comment or review body. They do not read the whole command.
Bodies are found as `-m`/`--body`/`--title` values, as heredocs, or as body
files written earlier by the same command or by a Write in the same turn. A
body the grader cannot find is not judged; it is neither a pass nor a fail.

## Slips no mechanical grader sees

- **A settled decision asked again** (#230, coder/websocket). Telling this
  apart from a fair question needs the decision record, so it is a judge
  grader's job. `synthetic/decision-reasked.json` passes every mechanical
  grader, which is how that gap shows.
- **A native reply that reached nobody** (#424). This was an engine defect
  (the ingesting bot held no ref for the replied-to message), not something
  the loop wrote. It belongs in a tier-2 test, and one exists.
- **"Terse enough for the group".** This is the one LLM-as-judge grader #450
  names. It is not built yet because it spends tokens (see below).

## Replaying a turn on another model

`replay` has a model play each case's turn again and writes what it did as
a case, which `grade` then scores like any other:

```
bin/spool-eval replay -model haiku -from 0 -count 10 eval/cases   # one batch, into eval/replays/
bin/spool-eval grade eval/replays
```

- **The model is shown** the loop's system prompt from `cases/prompts/`
  (export writes it with the hub's own `loop.SystemPrompt`; pass
  `-version` the hub's version so the prompt names the same build), and
  the case's envelopes as its turn input.
- **What it sends.** Each case's prompt and envelopes go to the Anthropic
  API under the operator's claude login, where the recorded turn went
  first. So the case files stay on the host, but their content does not.
- **Nothing real is reached.** The CLI gets no built-in tool (`--tools ""`),
  no MCP server but the two stubs (`--strict-mcp-config`), no project
  settings, and an empty working directory. The stubs offer the hub's own
  `send_message`, schema and description, answering with a made-up ref, and
  a `Bash` that runs nothing and answers "(no output)". After `-max-calls`
  calls (8) the stubs refuse the rest, and `-budget-usd` (0.25) caps each
  case's spend.
- **It spends the operator's plan tokens.** Run it only in batches, with
  the operator's go, posting each batch's total (printed last) before the
  next (#450).

What a replay cannot show:
- **Context the session had.** A replay starts a fresh session, so it lacks
  the earlier turns the recorded one could read.
- **Anything that depends on real tool output.** A command answers nothing,
  so a turn that reads a PR before writing to it reads nothing.
  `links_resolve` and a re-asked decision mean little on a replay.
- **The user's own `~/.claude` instructions** may still load. `--bare`
  would drop them, but it takes an API key, not a plan login. Run replay
  as a user whose `~/.claude` holds no instructions of its own.

## Not built yet

The judge grader ("terse enough for the group") lands with the baseline,
inside the same token cap.
