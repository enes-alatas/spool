# ADR-0060: A hub holds several Claude logins, and a loop runs on one of them

Date: 2026-10-23 · Status: accepted (operator decisions of 2026-10-23, recorded on #926: items 3, 4, 5, 6 and 8) · Amended: 2026-10-27, 2026-10-28, 2026-10-31 · Amends: ADR-0044 item 2, ADR-0047 items 3, 5, 7, 8 and 9, ADR-0058 items 1, 2, 3, 6 and 7 · Relates to: ADR-0043, ADR-0046, ADR-0048, ADR-0059

## Context

ADR-0058 made the *Claude login* one claude.ai sign-in per hub:
- the Claude proxy serves it to every contained loop;
- cloud loops run on it (ADR-0059 item 8);
- the login check runs on it (ADR-0044).

The hub keeps one plan-usage record, and the plan cap (ADR-0047) sleeps
every loop when that one plan is over. So a team whose people each hold
a Claude subscription can't run their loops on their own plans in one
fleet, and one busy loop puts every other loop to sleep (#925, #926).

Today the only way round it is an `env-var` connection on
`CLAUDE_CODE_OAUTH_TOKEN`. That puts a raw credential in the
workstation, which ADR-0058 item 1 exists to avoid, and nothing
refreshes it.

## Decision

1. **The hub holds named Claude logins.** A Claude login has:
   - a name, under the loop-name rule;
   - the `oauthAccount` it signed in to, which gives its email and
     organization;
   - its credential pair, sealed (ADR-0046);
   - the user who added it, its *owner*;
   - its last login check.

   Each login is signed in, refreshed, signed out and checked as ADR-0058
   items 3, 4, 6 and 7 and ADR-0044 do for the one login today: one refresh
   at a time per login, independently of the others. Settings lists the
   logins, and adding one runs the same CLI sign-in. The pages say
   *Claude login*, and name a login by its name. They don't call it an
   account.

2. **The login named `default` is the default, so a one-subscription hub
   is unchanged.**
   - The upgrade turns today's sign-in into a login named `default`.
   - A stored setup-token keeps serving as the default while no login
     named `default` is signed in (ADR-0058 item 5).
   - A loop that names no login runs on the default.
   - **Sign out is never refused.** It holds the loops on that login, as
     ADR-0058 item 6 holds every contained loop today, until the login is
     signed in again or the loops are moved, and the Sign out confirm
     names them. So a one-subscription hub can still sign out, and an
     owner can withdraw their own login at any time.
   - **Removal is refused (409) while a loop runs on the login,** the
     default counting every loop that names no login, as a connection a
     loop holds is (ADR-0043). A signed-out login still exists, so its
     loops' `claude_login` stays valid, and the 409 names the loops to
     move.

3. **A contained loop names its login.**
   - The loop record carries `claude_login`, and empty means the default.
   - The Claude proxy picks the token by the calling loop's login at
     each request, so a refresh or a move reaches the next request.
   - Moving a loop to another login closes its process at its next quiet
     boundary, as an `env-var` change does (ADR-0043's #640 amendment).
     Its session is kept. The slice that builds this probes a resumed
     session on another login's token first, and records what it finds
     in fakeclaude's FIDELITY.md.
   - **Bare loops** run on the host's own login, as today, so naming a
     login on one is refused (409).
   - **Cloud loops stay on the default login** in this epic (operator
     decision of 2026-10-23). A cloud environment (ADR-0059 item 6)
     belongs to the account it was made under, so a cloud loop on
     another login needs an environment from that login's account. Until
     #1078 builds that, naming a login on a cloud loop is refused (409),
     and the refusal names #1078.

   **Amendment (2026-10-31, #1078):** a cloud loop names its login too,
   by ADR-0061 item 7's rule.
   - The 409 is lifted. Its sessions start and take their turns under
     that login's token, in an environment of that login's account: one
     its list doesn't show is refused, and on a login other than the
     default the hub lists again before each start and starts only in
     one the list shows.
   - Unlike a contained loop's, a cloud loop's move waits for it to
     stop and drops its session, which belongs to the old account. It
     revokes the loop's hub token, which the old account's environment
     keeps as its secret, so the loop waits for Rotate token. Its
     setup check stands again only after a pass on the new login.

4. **Plan usage is kept per login.** A loop's rate-limit event is
   recorded against the login it ran on.
   - ADR-0047's #1052 rule (within a window, usage only rises) holds per
     login. A steered loop can then raise only its own login's usage;
     today it can cap the whole fleet.
   - A login with no observation reads unknown, as the fleet does today
     (ADR-0047 item 4) (operator decision of 2026-10-23).
   - Cloud turns still report no usage (ADR-0059 item 7).

5. **The plan cap works per login.** A login whose window is over caps
   only the loops on it, until that login's *until*.
   - The two thresholds stay hub-wide settings and apply to each login
     (operator decision of 2026-10-23). A threshold per login can come
     later, as its own issue.
   - Resume now takes a login, and the hold it stores is that login's.
   - Cloud loops follow the default login's cap, as today they follow
     the fleet's.

   **Amendment (2026-10-28, #1075):** bare loops report into the default
   login's plan usage (item 4), and follow its cap (operator decision of
   2026-10-28).
   - A bare loop runs on the host's own login (item 3), which the hub
     doesn't hold and can't name. Its usage has to go somewhere, and
     before this ADR it went into the fleet's one record with every
     other loop's.
   - On the usual hub the host's login is the operator's, and so is
     `default`. Counting the two as one plan keeps a one-subscription
     hub exactly as it was.
   - Where the host's login is another subscription, a bare loop and the
     loops on `default` can cap each other. A record of its own for the
     host, or no plan cap on bare loops, can come later as its own
     issue.

   **Amendment (2026-10-31, #1078):** a cloud loop follows the cap of
   the login it names (item 3's #1078 amendment), the default's when it
   names none. Its turns report no usage (ADR-0059 item 7), so only the
   login's other loops move it.

6. **Who may do what** (operator decision of 2026-10-23; the roles are
   ADR-0048's).
   - Any signed-in user, members included, may add a Claude login, and
     becomes its owner. A login is its owner's own subscription, and a
     member adding theirs is the team case this ADR is for.
   - A login's owner, or an admin, may sign it out and remove it.
   - Putting a loop on a login, or moving it, takes an admin, since a
     member changes no loop's config today. They may use their own
     login, the default, or another user's login only with that user's
     say-so. Nothing enforces the say-so until #100: the docs and the
     pages state it.
   - A loop running only on its own user's login waits for #100, which
     gives loops owners.

   **Amendment (2026-10-27, #1148):** two operator decisions of
   2026-10-27, from the slice's security review.
   - Removing a user signs out the logins they own: each is revoked at
     the source and kept, as Sign out is, so its loops keep a valid name.
     A removed owner can no longer withdraw it themselves, and no admin
     should put loops on a departed person's subscription.
   - Every user sees each login's name, owner and signed-in state. The
     address it signed in with is shown only to its owner and the
     admins, the default's only to the admins.

7. **The API.**
   - `GET` and `POST /api/claude-logins` list and add logins.
     `DELETE /api/claude-logins/{name}` removes one. Sign-in, check and
     sign-out are under `/api/claude-logins/{name}/`.
   - The loop view, and the loop's create and `PATCH`, carry
     `claude_login`.
   - `GET /api/plan-usage` adds `logins`: one entry per login in today's
     per-window shape, each with its own `cap` and `resumed_until`.
   - While the web halves move over, the top-level plan-usage fields stay
     as the default login's, and the single-login routes act on the
     default. Both go in the epic's last hub slice (#1077), once nothing
     in the control room reads them. Anyone else reading them breaks
     there, so it lands as a change of its own.

   **Amendment (2026-10-28, #1174):** checking the default login also
   checks the cloud account (ADR-0056 item 6), since the default is the
   cloud loops' account. That check's result is the note under the
   default's row in Settings → Claude login, and the Cloud page shows it
   too. Another login's check doesn't run it, and so the row's Check
   again checks everything with one route once the single-login check
   route goes (#1077).

   **Amendment (2026-10-28, #1077):** the removal has landed.
   - The four `/api/settings/cloud/account` routes are gone. They signed
     the default in (`sign-in`, `code`), checked it (`check`) and signed
     it out (`DELETE`). The default now does all of that under
     `/api/claude-logins/default/`, as any login does.
   - `GET /api/plan-usage` serves `logins` alone.
   - `POST /api/onboarding/harness-check` stays: it is ADR-0044's login
     check of the hub's Claude token, not one of the single-login routes.

8. **The docs say whose subscription a login is** (operator decision of
   2026-10-23). The Add page, OPERATIONS.md and the README say:
   - each login is the subscription of the person who signs it in, used
     by the loops they run;
   - Spool never pools or resells capacity (VISION);
   - the operator is responsible for following the terms of their plan.

   VISION's one-login line widens to one login per loop.

9. **The `CLAUDE_CODE_OAUTH_TOKEN` connection stays** as ADR-0058 item 1
   has it: a loop given one uses it directly, and none of this ADR applies
   to that loop. The docs point to a named login as the contained way to
   run a loop on another subscription.

10. **It ships in slices, each a hub half and its web half,** as children
    of #926:
    - **#1069 and #1070:** the logins themselves (items 1 and 2), and
      Settings listing them.
    - **#1071 and #1072:** a loop naming its login (items 3 and 6), and
      New loop and the loop page choosing and showing it.
    - **#1073 and #1074:** plan usage per login (item 4), and the Fleet
      page showing each.
    - **#1075 and #1076:** the plan cap per login (item 5), and the cap
      banner naming the login.
    - **#1077:** the removal in item 7, last.

    Each slice carries the docs of record for what it ships. Item 8's
    docs go with #1069 and #1070. #1078, under #860, covers cloud loops
    on another login.

## Consequences

- **A one-subscription hub sees no change.** Its login becomes
  `default`, every loop runs on it, and plan usage and the cap read as
  they do today.
- **One login's plan no longer sleeps the fleet.** A busy loop spends
  only its own login's windows. A steered loop's reports can raise only
  its own login's usage, which narrows the cross-loop reach THREAT-MODEL
  records for the plan cap.
- **The hub holds more credentials, each as sealed as the one today.**
  A copy of the data directory with the hub key hands over every login
  rather than one. THREAT-MODEL says so with #1069. No login enters a
  workstation: the proxy stays the one path for contained loops.
- **Refresh work grows with the number of logins.** Each one runs
  `claude auth status` near its own expiry, so a hub with *n* logins
  runs *n* refreshes every eight hours, not one.
- **Cloud loops are left on one account** until #1078. A team member's
  own subscription can run their contained loops but not their cloud
  ones.
- **Tier 2** needs fakeclaude to sign in more than one login, and the
  proxy's fixture to tell their tokens apart. Each slice adds what it
  uses.
