# ADR-0048: The hub has users, who sign in with a username and password

Date: 2026-10-09 · Status: accepted (operator decisions of 2026-10-04, 2026-10-07 and 2026-10-09, recorded on #582) · Amended: 2026-10-09 (item 1: the roles are enforced, #677); 2026-10-10 (item 1: a member reads no pairing code, #689); 2026-10-10 (item 7: owners manage users on the API, #690) · Supersedes: ADR-0030 decision 5 in part (the cookie is a session, not the token) · Relates to: ADR-0046

## Context

ADR-0030 gave the API one credential, the operator token, and the control
room trades it for a session cookie whose value is the token. That is one
anonymous operator. VISION's L5 wants owners, admins and members sharing a
fleet, each accountable for what they do, and the operator wants it on a
local hub too, not only on the hosted one.

Two properties of ADR-0030's cookie stand in the way. It names nobody, so
nothing a person does can be put down to them. It also revokes nothing: the
cookie is the token, so logging out clears one browser's copy, and a cookie
copied elsewhere works until the token itself changes.

Enes decided the shape on 2026-10-04: a username and password even on a
local hub, several users on one hub, and one fleet shared by roles rather
than a fleet per user, since separate fleets are L7's workspaces. He decided
the details on 2026-10-07 and 2026-10-09. They are recorded on #582 and
gathered here. This ADR is the hub half of slice 1. The sign-in pages are
#674, and roles are enforced in a later slice.

## Decision

1. **A user is a name, a role, a password hash, and whether a password
   change is due.**
   - The name follows the rule loop names follow: lowercase letters, digits,
     `-` and `_`, 2 to 32 characters, unique on the hub.
   - The role is `owner`, `admin` or `member`. Until the roles slice
     enforces a permission table, every signed-in user can do everything,
     and the role is stored and served but not checked.
   - The last owner can't be removed.

   **Amendment (2026-10-09, #677):** the roles are enforced. Enes decided
   the permission table on 2026-10-09 and control_room's reach on
   2026-10-10; both are recorded on #677.
   - **A member** reads the fleet and talks to its loops: every reading
     route except those below, plus messaging and waking a loop, posting to
     a channel or the group, and uploading an attachment to send.
   - **A member reads no owner DM** until #100 maps hub users to the loops
     they own. Owner DMs are left out of every message list and stream, and
     a file one carried answers 404. A loop's events, its turns and its
     live agent stream can hold an owner DM's text anywhere in them, so
     they're for admins and owners only.
   - **`control_room` is every user's thread with a loop** until #100, not
     the operator's alone: members read it and post into it, because that's
     how they talk to a loop. A signed-in user's message carries their own
     name as its author, whatever the request says. Only the operator token
     posts as `operator`. #682 changes the loop prompt's wording, which
     still calls the thread private.
   - **An admin** can call every route. **An owner** can too, and differs
     from an admin only in managing users, which comes with the Users
     section on Settings. The operator token acts as the owner.
   - A refusal is 403 `forbidden_role`. Every route is listed in one table
     in `internal/httpapi`. A route the table doesn't name needs an admin,
     so a route added later stays closed to members until someone decides
     otherwise.

   **Amendment (2026-10-10, #689):** a member reads the Telegram and Slack
   senders lists without their pairing codes. The code tells an admin that
   the person vouching for a pending sender got the bot's DM. A member who
   read every code could vouch for any sender, and only an admin or an
   owner can allow one. Admins, owners and the operator token still read
   every code.

2. **Passwords are hashed with PBKDF2-SHA256 from the Go standard
   library.** That means 600,000 iterations and a random 16-byte salt per
   user, so no new dependency. A password is 12 to 128 characters, with no
   composition rules, in line with NIST SP 800-63B. A new password can't be
   the one-time password it replaces.

3. **A one-time password is printed once and must be changed at first
   sign-in.**
   - It's issued when the hub first starts with no users, which creates
     owner `admin`, and by `spool user add` and `spool user reset`.
   - Each one is 20 random characters from an unambiguous alphabet. It goes
     to stdout once, beside where the operator token is printed, and is
     never logged. That includes the first start after this ships on an
     existing hub: its loops are untouched, and only `admin` and its
     one-time password are new.
   - While a user's change is due, every API route answers 403
     `password_change_required` except these: `/api/me`,
     `/api/me/password`, `/api/logout`, and the routes that need no
     credential. The hub enforces this itself, not only the control room.

4. **Sessions live in the database and can be revoked.**
   - Signing in creates a session row and sets `spool_operator` to a random
     32-byte session ID. The row keeps only the ID's SHA-256, so a copy of
     the database holds no live session.
   - A session ends after 14 days without use, or 30 days after sign-in,
     whichever comes first. Use is recorded at most once a minute, so a
     busy control room doesn't write on every request.
   - Every session a user holds ends when their password is reset, or when
     they're removed. A password change ends every session but the one
     making it.
   - `POST /api/logout` deletes the session's row as well as the cookie.
   - The hub sweeps hourly for sessions past either limit whose cookie never
     came back, so abandoned rows don't pile up.
   - The cookie keeps ADR-0030's attributes (`HttpOnly`,
     `SameSite=Strict`, and `Secure` over TLS), and the database
     makes the session stateless for the app tier: any hub process over
     the same store validates the cookie with one lookup.

5. **Sign-in is throttled per username.** After 5 failed attempts in a row,
   the name is locked for 30 seconds. Each further failure doubles the lock,
   up to 15 minutes, and a successful sign-in resets the count. A name with
   no failure for a day starts over.
   - The count is kept by the name tried, whether or not a user has it, so
     an unknown name locks exactly as a real one does. A name the name rule
     forbids is not counted, since no user can have it.
   - Each failure is added in one statement, so guesses sent in parallel
     each count. Only the guesses already in flight when the lock lands are
     still checked.
   - A locked name answers 429 `throttled`, with `retry_after` in seconds and
     a `Retry-After` header.
   - An unknown name answers exactly as a wrong password does, 401
     `bad_credentials`. Every answer, locked or not, comes after the same
     hashing work, so neither the answer nor its timing shows which names
     exist.
   - A wrong current password on `POST /api/me/password` counts against the
     user's name as a failed sign-in does, and a locked name refuses the
     change. A stolen session can't be used to guess the password without
     limit, and a guessed password would outlive every way a session ends.

6. **The operator token stays as the owner's credential for the CLI and
   automation, with no end date.** A request bearing it in an
   `Authorization` header acts as the owner, as it does today.
   - For one more release, `POST /api/login` also takes the token. That
     creates a session with no user, which acts as the owner, and a cookie
     still holding the token itself keeps working. Both go in the release
     after the one that ships this, after which a browser session always
     belongs to a user.
   - A session opened with the token keeps the token's SHA-256, and ends
     when the hub's token is no longer that one. Replacing the token still
     signs every such browser out, as it did when the cookie was the token.
   - Until then, the room shows such a session as "operator token".

7. **The API.**
   - `POST /api/login` takes `{username, password}` or `{token}`, and
     answers with the `/api/me` view.
   - `GET /api/me` is `{name, role, must_change_password, via}`, where `via`
     is `password` or `token`. A token session reads an empty name, the
     owner role, and no change due.
   - `POST /api/me/password` takes `{new_password}`, plus `current_password`
     once no change is due, and answers with the `/api/me` view. It refuses
     with:
     - 400 `password_too_short`, `password_too_long` or `password_reused`;
     - 401 `bad_credentials` for a wrong current password;
     - 429 `throttled` while the user's name is locked;
     - 400 `no_user` on a token session.

   **Amendment (2026-10-10, #690):** an owner manages users on the API, and
   no one else may: an admin who could raise anyone, themselves included,
   would be an owner in all but name. The routes run the rules `spool user`
   runs, from the same code.
   - `GET /api/users` is `[{name, role, must_change_password, created_at}]`,
     never a hash.
   - `POST /api/users` takes `{name, role}`, `role` defaulting to `member`,
     and answers 201 with the user and their `one_time_password`.
     `POST /api/users/{name}/reset` answers the same way and ends the user's
     sessions. A one-time password is in that response alone, which is
     `Cache-Control: no-store`, and in no log.
   - `PATCH /api/users/{name}` takes `{role}`. `DELETE /api/users/{name}`
     removes a user and ends their sessions.
   - Granting admin or owner, by adding a user with that role or by raising
     one to it, takes `current_password` too: the acting owner's password,
     or the operator token for a caller acting with the token. A stolen
     session alone can't make its thief an admin. A wrong one counts toward
     the owner's sign-in lock. Lowering a role, a reset and a removal ask
     for no password.
   - They refuse with 400 `bad_name` or `bad_role`, 409 `user_exists`,
     403 `confirm_password` for a missing or wrong password, 429 `throttled`,
     404 for an unknown user, and 409 `last_owner` for removing or
     demoting the hub's only owner. The store checks that last rule in the
     statement that makes the change, so two owners demoting each other at
     once can't both get through.

8. **The CLI manages users against the data directory, as `spool token`
   does.**
   - `spool user add <name> [--role owner|admin|member]` and
     `spool user reset <name>` print a one-time password.
   - `spool user list` prints names, roles and whether a change is due.
   - `spool user remove <name>` removes a user and ends their sessions.

   Each command opens the store directly and works whether or not the hub is
   running, since a session is checked against the store on every request.
   Users on Settings come in a later slice.

## Consequences

- An upgraded hub prints a one-time password once. An operator who misses it
  runs `spool user reset admin`, which needs only the data directory, the
  same reach `spool token` has always needed.
- Signing out and changing a password now end sessions. A copied cookie
  stops working when its session ends, which ADR-0030's cookie never could.
- Every API request with a session cookie costs one indexed lookup, and at
  most one write a minute.
- Until the roles slice, a member can do what an owner can. The role exists
  so the permission table has something to read, not as a boundary yet.
- The token's browser sign-in is a migration path with an end date. The
  release that removes it says so in its notes.
