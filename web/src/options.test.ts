import { describe, expect, it } from 'vitest'
import type { ModelList } from './api'
import { customModelNote, EFFORT_OPTIONS, modelOptions, RUNTIME_OPTIONS, runtimeNote } from './options'

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

const resolution = { source: 'probe' as const, cli_version: '2.1.281', resolved_at: 1 }

describe('modelOptions', () => {
  // A version in the fallback goes stale with the next Claude Code release;
  // versions come from the hub, never from this build (#289, #332).
  it('offers the default and the four families, and no version, before the hub answers', () => {
    const options = modelOptions()
    expect(options.map((o) => o.value)).toEqual(['', 'fable', 'opus', 'sonnet', 'haiku'])
    for (const o of options) expect(o.label).not.toMatch(/\d/)
  })

  it('says what each family runs as, in the order the hub gives', () => {
    const list: ModelList = {
      aliases: [
        { model: 'opus', resolved: 'claude-opus-5-5', ...resolution },
        { model: 'haiku', resolved: '', ...resolution, source: '' },
      ],
      custom: [],
    }
    expect(modelOptions(list)).toEqual([
      { value: '', label: 'Default (claude config)' },
      { value: 'opus', label: 'opus · claude-opus-5-5' },
      { value: 'haiku', label: 'haiku · fast & cheap' },
    ])
  })

  it('shows a family this build has no words for by its name', () => {
    const list: ModelList = {
      aliases: [
        { model: 'lyric', resolved: '', ...resolution, source: '' },
        { model: 'verse', resolved: 'claude-verse-1', ...resolution },
      ],
      custom: [],
    }
    expect(modelOptions(list).slice(1)).toEqual([
      { value: 'lyric', label: 'lyric' },
      { value: 'verse', label: 'verse · claude-verse-1' },
    ])
  })

  it('offers the custom models after the families, by label then id', () => {
    const list: ModelList = {
      aliases: [{ model: 'opus', resolved: 'claude-opus-5-5', ...resolution }],
      custom: [
        {
          id: 'cm_1',
          model: 'claude-opus-4-1',
          label: 'Opus 4.1 (pinned)',
          resolved: 'claude-opus-4-1',
          ...resolution,
        },
        { id: 'cm_2', model: 'claude-sonnet-4-5', label: '', resolved: '', ...resolution, source: '' },
      ],
    }
    expect(modelOptions(list).slice(2)).toEqual([
      { value: 'claude-opus-4-1', label: 'Opus 4.1 (pinned) · claude-opus-4-1' },
      { value: 'claude-sonnet-4-5', label: 'claude-sonnet-4-5' },
    ])
  })
})

describe('customModelNote', () => {
  it('says nothing when an id runs as itself', () => {
    expect(customModelNote({ model: 'claude-opus-4-1', resolved: 'claude-opus-4-1' })).toBe('')
  })

  it('says when an id is not resolved yet, or runs as another', () => {
    expect(customModelNote({ model: 'claude-opus-4-1', resolved: '' })).toBe('not resolved yet')
    expect(customModelNote({ model: 'claude-opus-4', resolved: 'claude-opus-4-20250514' })).toBe(
      'runs as claude-opus-4-20250514',
    )
  })
})

describe('option labels', () => {
  // The operator's call (#331): no em dash in UI text. A label separates its
  // value from the description with a middle dot or a comma instead.
  it('carry no em dash', () => {
    for (const o of [...modelOptions(), ...EFFORT_OPTIONS, ...RUNTIME_OPTIONS])
      expect(o.label).not.toContain('\u2014')
  })
})
