# ADR-0027: Colour means something, or it is white

Date: 2026-09-16 · Status: accepted · Amended: 2026-09-17 (§9, units; §6 and §10, rendering)

## Context

The control room shipped with a "night-shift mill" palette: indigo surfaces,
linen text, an amber accent. The accent did four unrelated jobs at once — it
was the brand, the primary button, the selected navigation item, *and* the
signal that a loop was working. An operator scanning the room could not tell,
from colour alone, whether amber meant "this is Spool" or "this loop is busy",
because it meant both.

Underneath that, styling was per-component. Each page picked its own borders,
radii and card shells, so the visual system existed only as a habit. Removing
chrome page by page would have produced drift rather than a smaller system
(#70, under #68).

The operator's actual job here is watching a fleet: most of what is on screen
is inventory, and the small part that matters is *state*. A palette that
spends colour on identity has nothing distinctive left for the few things that
need the operator to look.

## Decision

1. **One near-black canvas.** `#050505`, with a single raised value `#0c0c0e`
   for row hover and input wells. There is no second surface colour and no
   outer dashboard shell; structure comes from hairlines, spacing and
   alignment.
2. **White owns identity, primary text, primary action, and selection.** The
   Spool mark, the `+ New loop` button and the selected destination are all
   `#f2f1ed`. Colour never says "this is us" or "you are here".
3. **Colour is reserved for system state**, and each hue means one thing:
   - `#ff8a1f` — work happening now, or attention wanted soon (a busy loop, a
     running power control, a wake that is due, a context window filling).
   - `#3fe39a` — healthy, ready.
   - `#ff4567` — an error, a destructive action, a boundary already reached,
     or a standing unsafe condition.
4. **A deliberate state is not a fault, but a deliberate risk is still a
   risk.** Paused, asleep and powered-off are the operator's own decisions and
   render neutral — outline and muted grey, never red. That is the visual half
   of the rule ADR-0021 established for `workstation_off`. Red is not reserved
   for what went wrong; it is for what is wrong *or unsafe*, whether or not
   anyone chose it. So a bare loop keeps the red `uncontained` badge: those
   deliberate states all *remove* exposure, while running with no wall around
   the loop (ADR-0017) *adds* it, and it persists until someone changes it.
   Being chosen is what makes the badge worth drawing, not what excuses it.
5. **Filling and progress are data, not alarm.** Meters draw in the text
   colour at low opacity, and only take colour as they approach a limit.
6. **Typography carries the hierarchy.** System sans for titles, prose,
   missions and navigation; system mono for the operational register — loop
   names, states, costs, times, branches, paths. No network fonts.
   *Amended 2026-09-17 (#132):* the sans stack asks for `'Segoe UI Variable
   Text'` before `system-ui`. Still a system face and still no download —
   `system-ui` resolves to Segoe UI on Windows, the 2012 design, while
   Windows 11 ships a newer one drawn with optical sizes that `system-ui`
   does not select. Machines without it fall through to exactly what they
   had.
7. **Contrast is a floor, not a preference.** Every text token clears 4.5:1
   against every background it can land on. The approved muted grey
   `#77777c` measures 4.58:1 on the canvas but 4.39:1 on the hover fill, so
   the token ships as `#7a7a7f` — 4.77:1 and 4.58:1, visually indistinguishable
   and above the floor on both.
8. **Focus is an outline, and information survives without motion.** Keyboard
   focus is a crisp high-contrast outline, never a glow. Anything animated —
   the busy glyph, the streaming cursor — still carries its meaning when
   `prefers-reduced-motion` stops it.

9. **Type is relative, spacing is not** (amended 2026-09-17, #107). Every
   `font-size` is a `rem`, so a reader's browser font-size preference scales
   the whole control room; gaps, padding, radii and hairlines stay in `px`,
   because they are not text and a 2px hairline is a 2px hairline at any text
   size. Dimensions that exist to *hold* text — a column track, a bar's
   height, the clearance above a fixed bottom bar — follow the text, since a
   fixed box around growing text clips it. Two places cap deliberately rather
   than scale without limit, and each says so where it is written: the bottom
   destination bar's labels, where five of them share the viewport's width,
   and the fleet row's state column, where an identifier would otherwise
   squeeze the loop name to nothing.

10. **A hairline is specified in CSS and drawn in device pixels, and the two
    are not the same thing** (added 2026-09-17, #132). At a fractional display
    scale a 1px rule cannot land on a whole device pixel: at 125% — the
    operator's own monitor — Chrome splits it across two rows at 78% and 56%
    strength, so a token measured at 1.19:1 arrives at 1.12:1 and the room's
    whole structure reads soft. Nothing in CSS controls the sub-pixel offset a
    row's edge lands on; a 0.8px border, exactly one device pixel there,
    measures identically. So the token carries the difference: `--hairline` is
    `#24242a`, chosen so the split rendering at 125% reaches the weight the
    design has at 100%, and it is a touch stronger than necessary at whole
    scales rather than absent at fractional ones.

## Consequences

- **The accent stops being decoration.** Orange on screen now always answers
  "what is happening", which is only useful because it no longer also answers
  "what is this". Every existing use had to be sorted into identity or state;
  the four that were identity became white.
- **Glow is enhancement, never information.** A state that glows is also a
  state that differs in fill or outline, so a display, a screenshot or a
  colour-blind operator loses nothing when the glow does not land.
- **Tokens are semantic, so misuse is visible in review.** `--canvas`,
  `--surface`, `--hairline`, `--text`, `--text-muted`, `--active`, `--ready`,
  `--danger`. A rule that reaches for `--danger` to draw something neither
  wrong nor unsafe now reads as wrong in the diff, which the old `--rust` did
  not — `--rust` drew the `uncontained` badge and `state-paused` alike, so it
  could not tell an operator's choice from a hazard.
- **Cards are on notice, not gone.** This ADR sets the tokens; panels keep a
  hairline and the raised fill until #69 and #71 replace the shell and Fleet
  with rows. That interim is deliberate — the alternative was one unreviewable
  change.
- **A second palette value moved, and for the same kind of reason.**
  `--hairline` went from `#1b1b1e` to `#24242a` (§10). The first move was a
  contrast floor, this one a rasterisation floor; both are cases of a value
  chosen on a screen rather than in the abstract, and both are recorded here
  rather than folded in quietly.
- **A palette value moved.** #70 approved `#77777c`; §7 ships `#7a7a7f`. The
  design intent is unchanged and the reason is measured, but it is a
  deviation from an approved value and is called out here rather than folded
  in quietly.
- **Enlargement has a ceiling in two places, and it is visible.** A bar with
  five labels across a 320px viewport cannot honour a doubled text size and
  still fit; capping there is a real limit on the accommodation, so it is
  written at the rule rather than left for someone to discover. Browser zoom
  is unaffected either way — it scales CSS pixels, so nothing about it is
  capped.
- **Actionable warnings keep their boundary.** Dialogs, destructive
  confirmations and alerts keep a tinted edge and wash, because removing their
  container would remove the thing that makes them read as a boundary.
