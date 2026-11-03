# ADR-0063: The hub reads a bot from prompts.chat, on the operator's paste

Date: 2026-11-02 · Status: accepted (operator decision of 2026-11-02 on #1262 for the import, and of 2026-11-03, in a direct message to Terra, for the outbound fetch's bounds) · Relates to: ADR-0028, ADR-0048 · Amended: 2026-11-03 (item 1: fetch by path, teams and the base flag, #1265); 2026-11-03 (item 4: a team drafted, then created from its draft, #1265)

## Context

prompts.chat publishes ready-made bots. Each bot has instructions,
routines, skills and the connectors it needs, served as public `.json`
with no sign-in. An operator who finds one there has no way to run it on
Spool except retyping it. #1262 imports it: the operator pastes a bot's
URL and gets a draft of the loop it would make.

The control room can't read the bot itself, since prompts.chat's answer
is cross-origin to it. So the hub fetches it. The hub has never made an
outbound request on a URL the operator hands it. Its only outbound calls
go to hosts it is configured with, such as Claude, GitHub, Telegram and
Slack. A route that fetches whatever URL it is given would let anyone
able to call it point the hub at its own network.

## Decision

1. **One host.** The hub fetches only from `prompts.chat`, by exact name
   (`internal/promptschat`). The pasted URL picks a bot and nothing else.
   It can be the bot's page, its `.md` or its `.json`, over http or
   https. The hub builds `https://prompts.chat/bots/<bot>.json` from the
   bot's path segment itself. Any other host is refused before any
   request, and so is a port, credentials in the URL, or a page that
   isn't a bot. The fetcher checks the host again, and follows no
   redirect.

   **Amendment (2026-11-03, #1265):** the fetcher now takes a path, not a
   URL, and refuses any path but a bot's `.json`
   (`/bots/<bot>.json`) or a team's (`/api/bots/teams/<team>`), each
   built from a matched path segment. A team import fetches both, from
   the same host. The fetcher reaches that host at `--prompts-chat-base`,
   which defaults to `https://prompts.chat`. Like `--github-api-base`,
   it exists so tier 2 can point the hub at a stand-in server, and it
   takes https, or http on loopback only. It is the operator's flag:
   nothing an admin pastes reaches it, and a pasted URL must still name
   `prompts.chat` by exact name.
2. **On the operator's request only.** The fetch runs from
   `GET /api/import/prompts-chat?url=`, an admin route like
   `POST /api/loops`, behind the operator listener's host and cross-site
   checks. No schedule, loop or surface triggers it.
3. **Nothing sent but the GET.** The request carries no cookie, no
   credential and no body, only `Accept` and a `User-Agent` of `spool`. It
   takes 15 seconds at most and reads 1 MiB at most.
4. **A draft, not a loop.** The route creates nothing. It answers:
   - `name`: the bot's slug, made a valid loop name;
   - `mission`: the bot's instructions, then a line naming the bot's
     page, then one line per routine with its trigger in words and its
     prompt, then the skills and connectors by name;
   - `runtime`: `cloud` when the bot needs the cloud alone, otherwise the
     hub's default;
   - `egress`: the hosts of the connectors whose host is fixed, each with
     the connector it came from, such as `github.com` and `api.github.com`
     for GitHub;
   - `source`: the bot's page.

   The egress list is a hint. Nothing is added to the allowlist
   (ADR-0028).

   **Amendment (2026-11-03, #1265):** a team is drafted the same way.
   `GET /api/import/prompts-chat/team?url=` fetches the team's `.json`
   and each member's, at most 12, and creates nothing. It answers:
   - a `channel` named after the team's slug;
   - its `description`, on one line;
   - one loop per member, named from the member's role (or its bot's slug
     when the role leaves no name), each with the bot's draft. Each
     mission ends with a paragraph naming the channel and the other
     loops, then a line for each handoff to or from the loop, by the
     other loop's name and the handoff's label.

   A loop or channel name the hub already has is suffixed `-2`, `-3` and
   so on. `POST /api/import/prompts-chat/team` creates the team from the
   draft as the operator edited it: the channel and every loop in it,
   with no fetch. It is an admin's route. It is all or nothing: every
   loop is checked before any is stored, and none starts until all of
   them and the channel are stored, so a failure on the way removes what
   it stored. A team's loops run on bare or docker, since a cloud loop
   needs a repo and an environment of its own. The import creates loops
   only through this second call, so item 5 holds for teams: an imported
   mission reaches a loop only as the operator read it in the draft.

   **Amendment (2026-11-03, #1266):** each drafted loop also lists its
   handoffs as fields, `handoffs`, in its mission's order: a `direction`
   (`to` or `from`), the other `loop` by its name in the draft, and the
   `label` on one line, empty when the team gave none. The list is empty
   for a loop with no handoffs. The room shows them from these fields
   rather than by reading the mission's words. The create takes nothing
   new: the mission the operator read is still all a loop is given.
5. **What the bot's text is.** It is someone else's writing, so it is
   untrusted. The only way it reaches a loop is through the mission the
   operator reads in the draft and chooses to create. From each field it
   reads, the hub drops:
   - control characters (Unicode `Cc`) but newline and tab;
   - format characters (`Cf`): bidi overrides, zero-width characters,
     and the tag characters that spell text a model reads and a textarea
     doesn't show;
   - both variation-selector blocks, U+FE00–U+FE0F and
     U+E0100–U+E01EF, whose supplement can hide bytes after any
     character.

   Nothing else is dropped. A character that looks blank, such as
   U+3164 or U+2800, is kept, since stripping by look would mangle real
   text. What is left is still someone else's text, which the operator
   reads and nothing else checks.

   A bot whose mission would be over 64 KiB is refused, since the
   mission goes into every turn of the loop. Routines become text in the
   mission, not schedules: a loop's cadence stays its own `next-wake`.
   Skills are named, not installed.

## Consequences

- An operator can start from a prompts.chat bot instead of retyping it.
  The New loop form fills itself from this route (#1264).
- The hub now opens connections to prompts.chat when an admin asks. A
  host that blocks the hub's outbound traffic answers `import_failed`
  (502). A hub that wires no fetcher answers `import_off` (503).
- prompts.chat sees the hub's address and the bot asked for. Nothing
  else about the hub or its operator is sent.
- A mission imported from a bot is a prompt the operator didn't write. It
  can ask the loop for anything the loop can do. Reading it before
  creating the loop is the only check. The draft names its source so the
  operator knows where the text came from.
- A change to prompts.chat's `.json` can break the import. A page
  without instructions is refused as not a bot, and nothing is
  half-imported.
- Teams (#1265) will reuse this fetch for `/api/bots/teams/<slug>`. That
  is the same host and adds no new one.
