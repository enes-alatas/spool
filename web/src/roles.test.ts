import { describe, expect, it } from 'vitest'
import { may } from './roles'

describe('may', () => {
  it('lets every role talk to loops', () => {
    expect(may('member', 'talk')).toBe(true)
    expect(may('admin', 'talk')).toBe(true)
    expect(may('owner', 'talk')).toBe(true)
  })

  it('keeps managing for admins and owners', () => {
    expect(may('member', 'manage')).toBe(false)
    expect(may('admin', 'manage')).toBe(true)
    expect(may('owner', 'manage')).toBe(true)
  })

  it('gives a session with no role nothing', () => {
    expect(may(undefined, 'talk')).toBe(false)
    expect(may(undefined, 'manage')).toBe(false)
  })

  it('gives a role it does not know nothing', () => {
    expect(may('guest' as never, 'manage')).toBe(false)
  })
})
