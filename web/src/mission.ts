// What the room knows about editing a loop's mission, apart from how it is
// drawn.
//
// A mission is the loop's instructions, so changing it is a prompt change:
// #162 settled that a prompt change reaches a running loop at its next
// session rotation, and #261 settled that saving a mission asks for that
// rotation rather than waiting for one. That costs the loop its
// conversational context, which is why the operator is told before they save
// rather than after.
//
// A loop with no running session pays none of that, and the API says so in
// the `rotation` it answers. The room tells both stories: the warning from
// what it can see before Save, the confirmation from what the server did.

import type { Rotation } from './api'

export interface MissionDraft {
  // Whether Save does anything. False for a draft the API would refuse and
  // for one that asks for no change.
  canSave: boolean
  // Why Save is off, in the operator's terms — a disabled button with no
  // reason beside it reads as a broken page. Empty when Save is on, and
  // empty when the draft is simply untouched: an operator who has typed
  // nothing is not being stopped by anything.
  reason: string
}

export function missionDraft(original: string, draft: string): MissionDraft {
  const next = draft.trim()
  if (!next) {
    // The API refuses this on create and the room refuses it here, because
    // the thing it would produce — a loop whose instructions are the empty
    // string — has no useful reading.
    return { canSave: false, reason: 'A loop needs a mission: this is every instruction it has.' }
  }
  // Compared trimmed, because trailing whitespace is what a textarea
  // collects, not what an operator meant to change.
  if (next === original.trim()) return { canSave: false, reason: '' }
  return { canSave: true, reason: '' }
}

// The sentence shown while the draft is open, before Save is pressed. It
// names the cost rather than the mechanism: an operator deciding whether to
// edit a mission wants to know what it does to the loop, not what a session
// is.
//
// It names the boundary rather than promising immediacy. A rotation fires at
// a quiet boundary (ADR-0022 decision 2; `actor.go`'s Rotate and the API's
// handleRotate both say so), and the handoff note in the next clause is
// itself written in a turn. A loop mid-turn finishes that turn under the old
// mission and runs the handoff turn on the old session — so "at once" would
// be the sentence that told the operator not to expect the turns they are
// about to watch.
//
// A loop with no session is told the other true thing. Every clause of the
// rotation sentence is false for it — there is no note, no fresh session and
// no context to lose — and a warning about a cost nobody pays is the kind
// the operator learns to skip.
export function missionSaveWarning(name: string, hasSession: boolean): string {
  if (!hasSession) {
    return `@${name} has no running session, so there is nothing to rotate: the mission is saved now, and is the one its next session is built from.`
  }
  return `Saving rotates @${name}'s session: the loop writes a handoff note at its next quiet boundary, then continues on a fresh session under the new mission, without its current context.`
}

// What the save did, in the server's terms rather than the room's guess.
//
// The warning above is chosen from the loop as the page last read it, and a
// loop starts and ends sessions while a textarea is open: by the time Save is
// pressed the session it warned about may be gone, or a loop it promised
// nothing for may have started one. So the result is reported from the
// `rotation` the API answers, which is the only account of what actually
// happened to the session.
//
// `none` draws nothing: Save is off for an unchanged draft, compared trimmed
// the same way the API compares it, so the client cannot produce that answer
// — and a "nothing happened" line for a save the operator did make would
// read as a failure.
export function missionSaveResult(name: string, rotation: Rotation): string {
  switch (rotation) {
    case 'queued':
      return `Saved. @${name}'s rotation is queued: it finishes the turn it is in, writes a handoff note, then continues on a fresh session under the new mission.`
    case 'no_session':
      return `Saved. @${name} had no session to rotate, so its next one starts under the new mission.`
    case 'none':
      return ''
  }
}
