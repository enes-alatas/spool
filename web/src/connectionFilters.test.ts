import { describe, expect, it } from 'vitest'
import type { Connection } from './api'
import {
  emptiedBy,
  filterConnections,
  filtering,
  NO_FILTER,
  readFilter,
  writeFilter,
} from './connectionFilters'

const conn = (name: string, extra: Partial<Connection> = {}): Connection => ({
  name,
  kind: 'env-var',
  has_secret: true,
  created_at: 0,
  ...extra,
})

const all = [
  conn('github', { loops: ['archivist', 'gardener'] }),
  conn('tracker', { kind: 'mcp-server', loops: ['gardener'] }),
  conn('gardener-token', { owner_loop: 'gardener', loops: ['gardener'] }),
  conn('old-key', { revoked_at: 5 }),
]
const names = (cs: Connection[]) => cs.map((c) => c.name)

describe('filterConnections', () => {
  it('hides the revoked unless asked', () => {
    expect(names(filterConnections(all, NO_FILTER))).toEqual(['github', 'tracker', 'gardener-token'])
    expect(names(filterConnections(all, { ...NO_FILTER, revoked: true }))).toHaveLength(4)
  })
  it('matches a name in any case, anywhere in it', () => {
    expect(names(filterConnections(all, { ...NO_FILTER, q: ' TOK ' }))).toEqual(['gardener-token'])
  })
  it('narrows by kind, scope and the loop holding it, owned or attached', () => {
    expect(names(filterConnections(all, { ...NO_FILTER, kind: 'mcp-server' }))).toEqual(['tracker'])
    expect(names(filterConnections(all, { ...NO_FILTER, scope: 'private' }))).toEqual(['gardener-token'])
    expect(names(filterConnections(all, { ...NO_FILTER, scope: 'shared' }))).toEqual(['github', 'tracker'])
    expect(names(filterConnections(all, { ...NO_FILTER, loop: 'archivist' }))).toEqual(['github'])
    expect(names(filterConnections(all, { ...NO_FILTER, loop: 'gardener', kind: 'env-var' }))).toEqual([
      'github',
      'gardener-token',
    ])
  })
})

describe('filtering', () => {
  it('counts what narrows, not showing the revoked or a blank name', () => {
    expect(filtering({ ...NO_FILTER, revoked: true, q: '  ' })).toBe(false)
    expect(filtering({ ...NO_FILTER, loop: 'gardener' })).toBe(true)
  })
})

describe('emptiedBy', () => {
  it('names the first filter past which nothing is left', () => {
    expect(emptiedBy(all, { ...NO_FILTER, kind: 'github-app', q: 'zzz' })).toBe('none is of kind GitHub App')
    expect(emptiedBy(all, { ...NO_FILTER, loop: 'archivist', q: 'track' })).toBe('none is named like "track"')
    expect(emptiedBy([all[3]], NO_FILTER)).toBe('every connection is revoked')
  })
  it('says when only hidden revoked ones match', () => {
    const app = conn('old-app', { kind: 'github-app', revoked_at: 5 })
    expect(emptiedBy([all[0], app], { ...NO_FILTER, kind: 'github-app' })).toBe('only revoked ones do')
    expect(emptiedBy([all[0], app], { ...NO_FILTER, kind: 'github-app', revoked: true, q: 'new' })).toBe(
      'none is named like "new"',
    )
  })
})

describe('the query', () => {
  it('round-trips, keeps other params and drops unknown values', () => {
    const f = {
      q: 'git',
      kind: 'github-app' as const,
      scope: 'shared' as const,
      loop: 'gardener',
      revoked: true,
      problems: true,
    }
    const params = writeFilter(new URLSearchParams('tab=x'), f)
    expect(params.get('tab')).toBe('x')
    expect(readFilter(params)).toEqual(f)
    expect(writeFilter(new URLSearchParams('q=a&revoked=1&problems=1'), NO_FILTER).toString()).toBe('')
    expect(readFilter(new URLSearchParams('kind=nope&scope=all'))).toEqual(NO_FILTER)
  })
})

describe('the problems filter', () => {
  const failed = { status: 'failed' as const, reason: 'unreachable', checked_at: 1 }
  const checked = [
    conn('broken', { kind: 'mcp-server', last_check: failed }),
    conn('fine', { kind: 'mcp-server', last_check: { ...failed, status: 'ok', reason: 'ok' } }),
    conn('untested', {
      kind: 'mcp-server',
      last_check: { ...failed, status: 'unverified', reason: 'secret_untested' },
    }),
    conn('never'),
  ]
  it('lists only the connections whose last check failed', () => {
    expect(names(filterConnections(checked, { ...NO_FILTER, problems: true }))).toEqual(['broken'])
  })
  it('narrows, and says so when nothing failed', () => {
    expect(filtering({ ...NO_FILTER, problems: true })).toBe(true)
    expect(emptiedBy(checked.slice(1), { ...NO_FILTER, problems: true })).toBe('none is failing its check')
  })
})
