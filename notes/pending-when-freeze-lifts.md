# Pending GitHub edits, held while Actions quota is out (2026-09-21)

Every issue edit/comment bills a minute via issue-guards; hold until Enes lifts the freeze.

- #153 item 2 (flip-day settings): add "enable private vulnerability reporting" beside secret scanning and push protection. SECURITY.md (#246) points finders at the Security tab button, which exists only when the setting is on. Blocking for #246.
- #246: Enes decided (2026-09-21, via Terra): private vulnerability reporting. Response promise not yet stated; assume "acknowledged within a week" unless he objects. Resolve the "Operator decision pending" line on the PR when the freeze lifts.
- #245 (method mismatch under /api/ answers 200) has no milestone; triage into L2.
- Iris's #239 web half: file a child issue if the PR needs a `Closes` target other than #239 (Terra's #244 was "Part of #239").
- Delete notes/l3-surface-seam-agenda.md (stale).
- Branch protection (#1) required checks, confirmed from ci.yml on origin/main 2026-09-21: `changes`, `checks`, `itest`. Not `guards` (issue-guards) or `report` (ci-health).
- Flip decided 2026-09-21 10:47: CI stays out until public; flip as soon as Quinn's attachment eye-check and deliverable 6 are done. Pre-flip list sent to Enes (ref:1282); final version after Quinn's counts.
