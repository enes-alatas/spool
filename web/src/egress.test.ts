import { describe, expect, it } from 'vitest'
import type { EgressView } from './api'
import { applying } from './egress'

const view = (patch: Partial<EgressView>): EgressView => ({
  enforced: true,
  built_in: [],
  extra: [],
  ...patch,
})

describe('applying', () => {
  it('is true while the proxy holds an older list', () => {
    expect(applying(view({ changed_at: 2000, applied_at: 1000 }))).toBe(true)
  })
  it('is false once the proxy has the current list', () => {
    expect(applying(view({ changed_at: 2000, applied_at: 2000 }))).toBe(false)
  })
  it('is false while the list waits for the next docker wake, with nothing to poll for', () => {
    expect(applying(view({ changed_at: 2000 }))).toBe(false)
  })
  it('is false on an open hub, where the list binds nothing', () => {
    expect(applying(view({ enforced: false, changed_at: 2000, applied_at: 1000 }))).toBe(false)
  })
  it('is false with no view yet', () => {
    expect(applying(undefined)).toBe(false)
  })
})
