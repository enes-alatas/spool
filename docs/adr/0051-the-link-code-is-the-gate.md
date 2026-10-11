# ADR-0051: A link code is the one way a person becomes an allowed sender

Date: 2026-10-11 · Status: accepted (operator decisions of 2026-10-11 on #728, posture reviewed by Elliot there; epic #729, slice #730) · Supersedes: the pairing code, ADR-0029 items 6 and 8 in part, and ADR-0050 decision 1's "only an allowed sender links" · Relates to: ADR-0026, ADR-0048, ADR-0049, ADR-0050

## Context

A person reached a loop in two steps, with two codes on two pages. A
stranger DMed a bot and got a pairing code, appeared as *pending* on the
Access page, and an admin typed the code in to allow them. Separately, a
hub user sent a link code from Settings → Account to link the account
(ADR-0050). Pairing ended in an allowed sender that no user answered for.

Enes decided on 2026-10-11 that the link code becomes the only way in, for
owners and admins as much as for members (#728). An owner creates the
user, the user mints a code, sends it to any loop's bot in a DM, and is
both linked and allowed on every bot. Elliot reviewed the posture there.
Once the code is the gate, anyone who can find a bot can reach it, so two
requirements come with it: the person must consent to what the link
grants, and wrong codes must be limited. Milo took both into the epic.

## Decision

1. **A link code is minted for one surface.** `POST /api/me/links/code`
   takes `{"surface": "telegram"|"slack"}`, and anything else is refused
   400 `bad_surface`. A code sent on the other surface links nobody, as an
   unknown one does. A user still holds one open code, which a new one
   replaces, and it lives 15 minutes as before.
2. **The code is checked before the sender gate, in a DM only.** On any
   loop's bot or app, a DM that is a code is taken up from anyone but a
   blocked sender: an allowed sender, a stranger, or a pending sender from
   before this ADR. A code in a group is just words. A blocked sender's DM
   reaches nothing, a code included.
3. **The person consents before anything is linked.** A good code is spent
   at once. Its sender is asked: "this links your Telegram account to
   *alice* in the control room. *alice* will read your private
   conversations with this hub's loops, and you can message every one of
   them. Reply YES within 5 minutes to link, or ignore this to leave it
   unlinked." Their YES, in a DM to any bot or app on that surface, links
   them and makes them an allowed sender. Until then nothing is linked or
   allowed. The user's `GET /api/me/links/waiting` lists the person, with
   the loop whose bot or app the code reached and `expires_at`, and
   `GET /api/me/links` keeps listing links alone. A person holds one
   question at a time, and a new code replaces it. A YES, case and
   trailing `.` or `!` aside, is no message to the loop while there is a
   question to answer. With none, an allowed sender's "yes" is an ordinary
   message.
4. **A link allows, and losing it disallows.** Confirming a link makes the
   person's `tg_senders` or `slack_senders` row `allowed`, and creates it
   if it is missing. A blocked row stays blocked, and an allowed one is
   left as it is.
   - A Telegram person linked by a user whose role is `owner` becomes the
     owner of every loop with none, as an admin allowing a sender made
     them (#73), and is who a new loop's owner defaults to. A link by an
     admin or a member makes them owner of nothing, and passes them over
     for that default: an admin's choice of sender was the safeguard, and
     a link is self-service (Enes, 2026-10-12 on #746). A loop's owner
     becoming a user is #731's.
   - A link removed, by the user or by another link replacing it on that
     surface, deletes the person's row, so they are a stranger again and
     may link again. Deleting a user does the same for each of their
     links. A blocked row is not deleted: a block outlives the link.
   - An owner is an allowed sender, so the loops the person owns are
     theirs no longer, as when an admin deletes a sender.
   - Senders allowed before this ADR, with no user, are left as they are.
     They keep working, shown as not linked (Enes, 2026-10-11 on #729).
5. **A stranger is answered with nothing.** A DM or group message from a
   sender who is neither allowed nor blocked goes nowhere. It gets no
   reply and creates no row, and its text is never kept. Pairing codes
   are no longer issued, and nobody new becomes *pending*. The hub logs a
   stranger at most once an hour, with the surface, their id and how many
   of their messages were dropped. It logs the text never, since a
   stranger's DM is untrusted and may hold anything, credentials included.
   A stranger hears only the question of decision 3 and its answer. A
   wrong code, an unknown one or one from the other surface, is
   unanswered. An allowed sender's wrong code is still told why, as
   before.
6. **Wrong codes are limited.** A code is about 49 random bits and lives
   15 minutes, so guessing one is not the danger. A spray of guesses is a
   store read each, and the answers tell a code that works from one that
   does not.
   - A sender with 5 wrong codes in an hour is not listened to for an
     hour after the fifth.
   - Past 50 wrong codes from strangers in an hour, no stranger's code is
     checked until the hour is out. An allowed sender's still is, so a
     spray cannot lock the hub's own people out.
   - A YES with no question open counts as a wrong code, so a YES spray
     costs what a code spray does.
   - The counts are in memory, so a restart forgets them. Against 49 bits
     that live 15 minutes, that is accepted.
   - The numbers are Terra's, within Elliot's shape on #728.
7. **Links made and removed are recorded for admins.** An append-only
   record, `GET /api/links/changes` (admin and owner, newest first, `limit`
   as `/api/changes` takes it), holds `{id, at, action, user, surface,
   person, loop}`, where `action` is `link` or `unlink`. `user` is the
   user's name as it was. `loop` is the loop whose bot or app a link's
   code reached, by its name as it was. The control room hears a change on
   the stream as `link`, carrying the user, the surface and the state:
   `waiting`, `linked` or `unlinked`. It reaches admins, and the user it
   names.
8. **The allowlist is hub-wide.** A link allows the person on every loop's
   bot or app, as allowing a sender always has (ADR-0026), and private
   reads are scoped by the link, not by the allowlist (ADR-0050). So any
   linked user can wake any loop and spend the operator's plan. That was
   already true of every allowed sender, so it is no regression. Scoping
   by loop would be a product feature, not a security one (#728 question
   3).

## Alternatives considered

- **The bot gives a stranger a code, and the user types it into the room**
  (Elliot's A2). That also proves consent, but every unknown DM gets a
  reply, which is spam and tells a stranger the bot is live.
- **Link without asking.** The code's issuer would read the private
  conversations of whoever sent it, and that sender would be allowed on
  every bot, on the strength of a code someone handed them. Consent is the
  one thing the code cannot prove alone.
- **Keep pending for strangers, without a code.** That keeps the Access
  page's pending list and its Allow button, an admin-made sender that no
  user answers for and nobody consented to. It is the state this ADR
  exists to end.
- **Limits kept in the store.** They would survive a restart, at a write
  per wrong code on a single-connection store, which is the cost the
  limits are there to cap.

## Consequences

- A fresh hub's owner reaches their loops by linking, not by pairing. The
  README's quick start and the demo video change with the release that
  ships this (#733), and the owner picker follows in #731.
- The Access page still lists, allows and blocks the senders a hub already
  holds, pending ones included, until Settings → Access replaces it
  (#732). Until then, Settings → Account asks for a code without naming
  a surface, which is refused: #732 names it, and shows a link waiting.
- An allowed sender whose user is deleted, or who removes their link, can
  no longer reach any loop, and owns none, until they link again.
- Restoring an older backup brings back the links and allows that were
  removed after it was taken. OPERATIONS.md says to check Access after a
  restore.
