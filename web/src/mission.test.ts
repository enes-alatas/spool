import { describe, it, expect } from 'vitest'
import { missionDraft, missionSaveResult, missionSaveWarning } from './mission'

describe('missionDraft', () => {
  it('saves a changed mission', () => {
    expect(missionDraft('Tidy the docs.', 'Tidy the docs, and the tests.')).toEqual({
      canSave: true,
      reason: '',
    })
  })

  // Saving rotates the session, so an accidental Save on an untouched draft
  // costs the loop its context for nothing.
  it('does not save a mission nobody changed', () => {
    expect(missionDraft('Tidy the docs.', 'Tidy the docs.').canSave).toBe(false)
  })

  // What a textarea collects at the end of a paragraph is not an edit.
  it('reads whitespace-only changes as no change', () => {
    expect(missionDraft('Tidy the docs.', '  Tidy the docs.\n').canSave).toBe(false)
  })

  // Nothing is wrong yet — the operator simply has not typed — so there is
  // nothing to tell them.
  it('gives no reason for an unchanged draft', () => {
    expect(missionDraft('Tidy the docs.', 'Tidy the docs.').reason).toBe('')
  })

  it('refuses an empty mission', () => {
    expect(missionDraft('Tidy the docs.', '').canSave).toBe(false)
    expect(missionDraft('Tidy the docs.', '   \n ').canSave).toBe(false)
  })

  // A disabled button with no explanation beside it reads as a broken page,
  // and this is the one case where the operator has done something.
  it('says why an empty mission cannot be saved', () => {
    expect(missionDraft('Tidy the docs.', '').reason).toContain('needs a mission')
  })
})

describe('missionSaveWarning', () => {
  // The rotation is the part an operator would not predict from the word
  // "Save", and the context loss is what it costs them.
  it('names the loop, the rotation and what it costs', () => {
    const warning = missionSaveWarning('gardener', true)
    expect(warning).toContain('@gardener')
    expect(warning).toContain('rotates')
    expect(warning).toContain('without its current context')
  })

  // Pinned, because this is the claim that drifts: a rotation fires at a
  // quiet boundary (ADR-0022), so the operator has at least one more turn of
  // the old mission to watch, and a sentence promising "at once" tells them
  // not to expect it.
  it('names the boundary rather than promising immediacy', () => {
    const warning = missionSaveWarning('gardener', true)
    expect(warning).toContain('next quiet boundary')
    expect(warning).not.toContain('at once')
  })

  // A loop with no session pays nothing for a save, and every clause of the
  // rotation sentence would be false for it.
  it('warns of no cost for a loop with no session', () => {
    const warning = missionSaveWarning('gardener', false)
    expect(warning).toContain('@gardener')
    expect(warning).toContain('nothing to rotate')
    expect(warning).not.toContain('handoff note')
    expect(warning).not.toContain('without its current context')
  })
})

describe('missionSaveResult', () => {
  // Queued, not done: the operator watching the loop will see it finish the
  // turn it is in under the mission they just replaced.
  it('reports a queued rotation without promising it has happened', () => {
    const result = missionSaveResult('gardener', 'queued')
    expect(result).toContain('@gardener')
    expect(result).toContain('queued')
    expect(result).not.toContain('at once')
  })

  // The case the warning cannot be sure of: the session may have ended while
  // the textarea was open, and then the save cost the loop nothing.
  it('reports a save that rotated nothing', () => {
    const result = missionSaveResult('gardener', 'no_session')
    expect(result).toContain('Saved')
    expect(result).toContain('no session to rotate')
    expect(result).not.toContain('queued')
  })

  // Unreachable from this panel — Save is off for an unchanged draft — and a
  // line saying nothing happened would read as a failed save.
  it('draws nothing when the server rotated nothing because nothing changed', () => {
    expect(missionSaveResult('gardener', 'none')).toBe('')
  })
})
