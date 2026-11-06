import { describe, expect, it } from 'vitest'
import type { LangfuseSettings } from './api'
import { changed, changeFrom, statusLine } from './langfuse'

const off: LangfuseSettings = {
  relay_upstream: '',
  read_address: '',
  ingest_key_set: false,
  read_key_set: false,
  exporting: false,
  default_relay_upstream: 'http://langfuse-web',
}
const on: LangfuseSettings = {
  ...off,
  relay_upstream: 'http://langfuse-web',
  read_address: 'http://127.0.0.1:3000',
  ingest_key_set: true,
  read_key_set: true,
  exporting: true,
}
const untouched = { upstream: null, read: null, ingestKey: '', readKey: '' }

describe('statusLine', () => {
  it('says off with no trace address', () => {
    expect(statusLine(off)).toMatch(/^Off/)
  })
  it('says an address without an ingest key sends nothing yet', () => {
    expect(statusLine({ ...off, relay_upstream: 'http://langfuse-web' })).toBe(
      'Not sending yet: add an ingest key.',
    )
  })
  it('says on when the hub says it exports', () => {
    expect(statusLine(on)).toMatch(/^On\./)
  })
  it('says bare loops send nothing without a read address', () => {
    expect(statusLine({ ...on, read_address: '' })).toBe(
      'On for docker loops from their next session. Bare loops send nothing until a read address is set.',
    )
  })
})

describe('changeFrom', () => {
  it('sends nothing for untouched fields or before the settings load', () => {
    expect(changed(changeFrom(on, untouched))).toBe(false)
    expect(changed(changeFrom(undefined, { ...untouched, ingestKey: 'pk:sk' }))).toBe(false)
  })
  it('sends nothing for an address edited back to what is stored', () => {
    expect(changeFrom(on, { ...untouched, upstream: ' http://langfuse-web ' })).toEqual({})
  })
  it('sends an emptied address as "" to clear it', () => {
    expect(changeFrom(on, { ...untouched, read: '' })).toEqual({ read_address: '' })
  })
  it('sends a pasted key trimmed, and never an empty one', () => {
    expect(changeFrom(on, { ...untouched, ingestKey: ' pk-lf-1:sk-lf-1 ', readKey: '  ' })).toEqual({
      ingest_key: 'pk-lf-1:sk-lf-1',
    })
  })
})
