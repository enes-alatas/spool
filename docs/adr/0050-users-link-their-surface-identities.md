# ADR-0050: A user links their own surface identity, and reads the private conversations that are theirs

Date: 2026-10-10 · Status: accepted (operator decisions of 2026-10-10, recorded on #100); decision 1 in part (who may link, and that a code links at once) superseded by ADR-0051 · Amends: ADR-0029 (item 8), ADR-0048 (item 1), ADR-0049 (decision 7)

## Context

A hub user (ADR-0048) signs in to the control room. A loop's private
conversations are with people on a surface: its owner on `owner_dm`, and
anyone else allowed on `dm:<name>` (ADR-0049). Nothing linked the two, so
a member read no private conversation at all, not even their own, and an
admin read every one.

Enes accepted all nine proposed answers to the design questions on
2026-10-10 (#100). This ADR records the three about users, decisions 6, 7
and 9 there. #703 proposed a link code that lives 15 minutes and links
once; nobody objected. The control room's side is #704's.

## Decision

1. **A user links their own identity on a surface by sending a code.**
   The control room gives the signed-in user a link code, and they send it
   to any loop's Telegram bot or Slack app in a DM. That links them to the
   sender. Nobody links someone else, an admin included: the link is
   proved by the person sending the code from their own account.
   - A code is `link-` and ten characters of the one-time password's
     alphabet. It links once and lives 15 minutes, and the hub keeps only
     its SHA-256. Asking for a new code replaces the open one.
   - A DM that is a code is not a message to the loop: it is neither
     stored nor delivered, and wakes nothing. The bot or app answers in
     that DM with a hub notice, which says who the account is now linked
     to, or that the code is unknown, used or expired. Case and the space
     around it are forgiven; a code inside other words is just words.
   - Only an allowed sender links. A pending sender's DM gets the pairing
     code as before, and a blocked sender's reaches nothing.

     *Superseded by ADR-0051 (2026-10-11, #730): a code is taken up from
     anyone but a blocked sender, ahead of the sender gate, and links only
     once the sender replies YES. Pairing codes are no longer issued.*
2. **A user has at most one link per surface, and a person is linked to at
   most one user.** A new code sent from another account on the same
   surface replaces the user's link there. A code sent from an account
   another user holds is refused and spent. A user removes their own link,
   and removing a user removes their links.
3. **A private conversation is read by its person, and by admins and
   owners.** This replaces "a member reads no owner DM" (ADR-0048).
   - A member reads the `dm:<name>` conversations of the people they are
     linked as, with any loop, since such a conversation is keyed to its
     person (ADR-0049 decision 6).
   - "The loops a user owns" are the loops whose owner, on the loop's own
     surface, is a person the user is linked as. A member reads what such
     a loop and they said on `owner_dm`, and not what it said with an
     owner before them: decision 7 reads a private conversation by its
     person, and decision 9 only says which loops are theirs. So an
     `owner_dm` message records the owner it was with, as a `dm:<name>`
     message records its person. One older than #703 records nobody and
     stays an admin's. Owning a loop grants nothing else.
   - Every other private message is kept from a member as before: left
     out of every message list and stream, with any file it carried
     answering 404. A loop's events, turns and live agent stream stay
     admin-only, since they can carry any private message's text.
   - The rule reads links as they are on each request, and on a stream as
     they are at each private message, so a link made or removed counts
     from then on.

## Consequences

- A colleague who talks to a loop on Telegram or Slack can read that
  conversation in the control room, and nobody else's.
- A loop that changes owner does not hand its private history over: the
  new owner's user reads `owner_dm` from the change on, and the earlier
  owner's user keeps reading what was theirs. Owner DMs from before #703
  stay with admins and owners, which takes nothing away, since a member
  read no `owner_dm` before.
- The API gains three routes, each about the signed-in user only:
  `POST /api/me/links/code`, `GET /api/me/links` and
  `DELETE /api/me/links/{surface}`. The operator token is no user, so it
  links nothing; it reads everything as an owner.
- A reaction or a vote on a private message still reaches every stream,
  as before: it carries ids, an emoji or a choice, and no text.
