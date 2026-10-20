import { describe, expect, it } from 'vitest'
import type { EgressView } from './api'
import { allowlistSyncText, applying, countHosts, filterHosts } from './egress'

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

describe('allowlistSyncText', () => {
  const now = 10 * 60_000
  it('says an environment is in sync, and since when', () => {
    expect(allowlistSyncText({ loop: 'gardener', in_sync: true, synced_at: now - 5 * 60_000 }, now)).toBe(
      'in sync · 5m ago',
    )
    expect(allowlistSyncText({ loop: 'gardener', in_sync: true }, now)).toBe('in sync')
  })
  it('names what failed, to the admin who is sent it', () => {
    expect(
      allowlistSyncText({ loop: 'gardener', in_sync: false, error: 'environment update: 503' }, now),
    ).toBe('not in sync: environment update: 503')
  })
  it('says only that it is out of sync to a member, who is sent no error', () => {
    expect(allowlistSyncText({ loop: 'gardener', in_sync: false, synced_at: now }, now)).toBe('not in sync')
  })
})

describe('filterHosts', () => {
  const hosts = ['registry.npmjs.org', '.npmjs.com', 'pypi.org']
  it('keeps every host under an empty filter, or one of spaces', () => {
    expect(filterHosts(hosts, '  ')).toEqual(hosts)
  })
  it('keeps the hosts holding the filter anywhere in the name, in any case', () => {
    expect(filterHosts(hosts, ' NPMJS ')).toEqual(['registry.npmjs.org', '.npmjs.com'])
  })
  it('keeps nothing when no name holds the filter', () => {
    expect(filterHosts(hosts, 'crates')).toEqual([])
  })
})

describe('countHosts', () => {
  it('counts the hosts across groups', () => {
    expect(
      countHosts([
        { reason: 'a', hosts: ['x', 'y'] },
        { reason: 'b', hosts: ['z'] },
      ]),
    ).toBe(3)
  })
})
