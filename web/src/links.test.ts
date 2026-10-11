import { describe, expect, it } from 'vitest'
import type { UserLink } from './api'
import { linkName, spent } from './links'

const link = (over: Partial<UserLink>): UserLink => ({
  surface: 'telegram',
  id: '700000001',
  username: '',
  display: '',
  created_at: 0,
  ...over,
})

describe('linkName', () => {
  it('says the handle and the name when the surface gave both', () => {
    expect(linkName(link({ username: 'sam_fixture', display: 'Sam' }))).toBe('@sam_fixture (Sam)')
  })

  it('says whichever the surface gave, and the id when it gave neither', () => {
    expect(linkName(link({ username: 'sam_fixture' }))).toBe('@sam_fixture')
    expect(linkName(link({ display: 'Sam' }))).toBe('Sam')
    expect(linkName(link({}))).toBe('700000001')
  })
})

describe('spent', () => {
  const minute = 60 * 1000
  // Asked for at 100 minutes, so it expires at 115.
  const code = { code: 'link-fixture0001', expires_at: 115 * minute }

  it('is spent by a link made since it was asked for', () => {
    expect(spent(code, [link({ created_at: 101 * minute })])).toBe(true)
  })

  it('is not spent by a link from before it, or by none', () => {
    expect(spent(code, [link({ created_at: 99 * minute })])).toBe(false)
    expect(spent(code, [])).toBe(false)
    expect(spent(code, undefined)).toBe(false)
  })
})
