---
title: Management tokens
weight: 6
---

A *management token* is the credential an operator's own agent holds to look
after the hub, in place of the operator token (ADR-0067). It opens only
the management MCP, below. An admin mints, lists and revokes them in
Settings → Management tokens (#1403), or over the API:

- `POST /api/management-tokens` with `{"name": "watcher"}` mints one and
  answers with it once, as plain text. Copy it then: the hub keeps only
  its hash and can't show it again. It ends in 90 days; send
  `"expires_at"` as a Unix time in milliseconds for another end, or `0`
  for none.
- `GET /api/management-tokens` lists them by name, owner, when each was made,
  when it ends and when it was last used, never by value.
- `DELETE /api/management-tokens/<id>` revokes one at once: its owner may,
  even once no longer an admin, and so may any admin. Removing a user
  revokes the tokens they minted.

The operator API refuses a management token on every route (401): it is not
a second operator token.

## Pointing an agent at the hub

The management MCP is `/manage/mcp`, streamable HTTP, with the token as a
bearer. It has three tools (ADR-0067):

- `list_loops`: each loop's name, runtime, state, whether it is paused
  and whether `resume_loop` can resume it, its health (`ok`, `warning`
  or `failing`) and when its latest turn started. No mission, message or
  secret.
- `pause_loop` and `resume_loop`, by the loop's name, as the control
  room's Pause and Resume. An agent resumes only a loop an agent paused:
  a person's pause, from the control room or the API, stays theirs to
  lift, and an agent pausing it again doesn't change whose it is.

Each pause and resume is on the loop's change record as
`manage:<token name>#<token id>`.

An agent on the hub's own machine, such as Claude Code in a terminal,
uses the operator listener:

```sh
claude mcp add --transport http spool-manage http://127.0.0.1:8080/manage/mcp \
  --header "Authorization: Bearer spool_manage_…"
```

An agent in a Claude Code cloud session uses the cloud listener's public
address instead, `https://<your address>/manage/mcp`, with the cloud
listener on (see Cloud loops). The loop listener doesn't serve it.

A token revoked or past its end is refused at the agent's next call. A
bare loop can read a token a local agent keeps in your files, as it can
read the operator token (THREAT-MODEL.md).
