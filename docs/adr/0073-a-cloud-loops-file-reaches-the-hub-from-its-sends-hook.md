# ADR-0073: A cloud loop's file reaches the hub from its send's hook

Date: 2026-11-22 · Status: accepted (the design on #1713, comment 7069320341, as edited at 21:16 UTC on 2026-11-21 with Elliot's security read folded in, comments 7069340847 and 7069365156; accepted by the operator on 2026-11-22, recorded in comment 7076956915, widening included) · Supersedes: ADR-0056 item 2's *Attachments*, for a file a cloud loop sends · Relates to: ADR-0026, ADR-0037, ADR-0056, ADR-0059, ADR-0061

## Context

A contained or bare loop attaches a file by path: the hub reads it out of
the workstation (`keepSent`, ADR-0037). A cloud loop's files are on
Anthropic's VM, which the hub can't read, so ADR-0056 refused its
`send_message` with `attach`. The operator asked for it on 2026-11-20,
after a cloud loop told him it couldn't send a file (#1713).

The VM can reach the hub: every request it makes to the hub's host
carries the loop's token, which the egress proxy adds as the
environment's network secret, so the VM never holds it (ADR-0061). The
setup script already installs one guard on that path: a `PreToolUse`
hook on `add_repo` that posts to `/hooks` and lets the call go ahead only
on a 204.

Read from the bundle of Claude Code 2.1.322 (comment 7076971488 on
#1713): a `PreToolUse` hook's input carries `session_id`, `cwd`,
`tool_name`, `tool_input` and `tool_use_id`, its matcher takes an MCP
tool's full name, and the CLI's MCP client puts the same id in every
`tools/call` as `_meta["claudecode/toolUseId"]`. The input's
`session_id` is the CLI's own, not the VM's `cse_…` id the hub records.

## Decision

1. **The bytes travel in a `PreToolUse` hook on the loop's own
   `send_message`, not in the tool call.** The setup script adds the
   matcher `mcp__spool__send_message` to the hook script it already
   writes. A call without `attach` posts nothing. For one with `attach`,
   `python3`, which the image has and the setup script already uses,
   parses the hook input, resolves `attach` against `cwd` with symlinks
   followed, and checks the result: a regular file, at most 20 MB, under
   the working directory or `$HOME` but not under `$HOME/.claude*` or
   `$HOME/.config`, where the VM's own session credentials are. It copies
   the file and its base name to fixed files in a temporary directory of
   the call's own, and curl posts those to `/hooks/attach` with the
   session in `X-Spool-Session` and the hook input as a field. No string
   the model wrote reaches a shell word or a curl argument. A 204 lets the
   call go ahead; anything else refuses it (exit 2) with the hub's reason
   as what the model reads, as the `add_repo` guard does. The
   `send_message` call that follows carries the file as a contained
   loop's does: the same row, so Telegram, Slack and the control room
   send it unchanged.

   Bytes inside the tool call would pass through the model as base64 it
   writes out, millions of output tokens for a few MB, and a binary file
   it can't read faithfully. An upload the loop runs itself, then a send
   naming its id, works without a hook but is two steps the model must
   get right, with teaching, an id to carry and a refusal for each half;
   the hook keeps the loop's side `attach: <path>`.

2. **Only the loop's own session uploads, in a turn of it.**
   `/hooks/attach` decides from headers alone, in this order: the token
   (`Authorization`), the shape of the session in `X-Spool-Session`,
   `MCPCall` (the loop's own recorded session, while a turn of it runs),
   and no other upload of the loop in flight. A post that fails any of
   them is refused before its body is read, under the cloud listener's
   1 MB bound. Once the body is in, the hook input must be a
   `PreToolUse` of `mcp__spool__send_message` with a `tool_use_id`, or
   nothing is kept. The input's `session_id` isn't compared with the
   header: it is the CLI's own id, not the VM's, so the session binding is
   the header's, from `CLAUDE_CODE_REMOTE_SESSION_ID`, as the `add_repo`
   guard's is.

3. **Bounds.** Nothing changes before the token. After the four checks,
   and only on `/hooks/attach`, the post's body bound rises to 20 MB and
   64 KB for the form, and its read and write deadlines to 2 minutes and
   a half, through `http.ResponseController`. One upload is in flight per
   loop: `holdsSecret` needs the whole text in memory, and the body, its
   text, the stripped copy and the redactor's output are each up to 20 MB,
   so memory stays at a few times 20 MB per cloud loop. At most 4 wait per
   loop; a fifth is refused.
   A file over `attach.MaxSize` is refused by the hook before a byte is
   sent, by the hub's bound, and by `files.Keep`, with words naming the
   limit. The hook's entry has a 180-second timeout, past the CLI's
   default of 60, and curl's is 150. curl tries a post twice more on a
   lost connection, a timeout, a 429 or a 5xx. A post sent again replaces
   the first, and a busy slot's 409 is not tried again.

4. **What is checked before a byte is kept** is what `keepSent` checks:
   the name is redacted as it arrives, before any log line names it; a
   file holding a secret the hub knows is refused with the same words;
   the kind comes from the bytes.

5. **A claim is scoped and used once.** An upload waits as an
   attachment row with no message, keyed in memory by its loop, session
   and `tool_use_id`. Only a `send_message` of that loop, from that
   session, with that id in its `_meta`, claims it, and the claim takes
   the entry, so a second send finds none. A tool use id belongs to one
   call of one turn, so the key holds it to that turn too. An unclaimed
   upload is removed when the loop's session posts its Stop, since the
   call its hook ran for comes right after the hook or not at all; the
   hourly sweep of unsent uploads, at `UploadWait`, is the backstop for a
   turn that ends without one.

6. **An environment on an older setup script** posts nothing, so its
   `send_message` with `attach` finds no upload waiting and is refused
   with words saying the environment's setup script is older than the
   hub's. The setup view already tells such a script apart (#1381).

7. **What the loop is taught:** the `attach` field's description names
   the session for a cloud loop, and a cloud loop's prompt says where a
   file may come from and that it goes as it sends.

Files a person sends to a cloud loop stay unsupported (`PutFile`).

## Consequences

- An authenticated, in-turn session of the loop may post up to 20 MB
  within 2 minutes to the cloud listener. Nothing reachable without the
  loop's token and session changes. The path rule is enforced in the VM
  only: a loop could post the form itself, as it could paste a file's
  text into words, so it is a guard against mistakes, not a boundary. The
  hub-side checks that hold are the session and turn binding, the bounds
  and the secret check; the file goes only where the loop may write.
- The setup script's digest moves: an environment needs the operator to
  paste it again before its loop can attach a file, and until then a
  send with a file is refused, and one without works as before.
