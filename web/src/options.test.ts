import { describe, expect, it } from 'vitest'
import { MODEL_OPTIONS, RUNTIME_OPTIONS, runtimeNote } from './options'

describe('runtimeNote', () => {
  it('warns about uncontained, in the words the README uses', () => {
    const note = runtimeNote('bare')
    expect(note.warn).toBe(true)
    expect(note.text).toMatch(/your own account/)
    expect(note.text).toMatch(/permission prompts bypassed/)
  })

  it('explains the container without warning about it', () => {
    const note = runtimeNote('docker')
    expect(note.warn).toBe(false)
    expect(note.text).toMatch(/own container/)
  })

  it('treats a kind it has not heard of as contained rather than warning at random', () => {
    // The hub names its own default; a kind this build does not know is not
    // evidence that the loop is uncontained, and a warning that fires on
    // anything unfamiliar is one an operator learns to ignore.
    expect(runtimeNote('').warn).toBe(false)
    expect(runtimeNote('podman').warn).toBe(false)
  })

  it('offers both kinds the API accepts', () => {
    expect(RUNTIME_OPTIONS.map((o) => o.value)).toEqual(['docker', 'bare'])
  })
})

describe('MODEL_OPTIONS', () => {
  // A version in the list goes stale with the next Claude Code release; a
  // full id belongs in the form's custom field (#289).
  it('offers the default and the four family aliases, and no version', () => {
    expect(MODEL_OPTIONS.map((o) => o.value)).toEqual(['', 'fable', 'opus', 'sonnet', 'haiku'])
    for (const o of MODEL_OPTIONS) expect(o.label).not.toMatch(/\d/)
  })
})
