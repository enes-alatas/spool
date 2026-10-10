import { describe, expect, it } from 'vitest'
import { grants, may } from './roles'

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

  it('keeps managing users for owners', () => {
    expect(may('member', 'manage-users')).toBe(false)
    expect(may('admin', 'manage-users')).toBe(false)
    expect(may('owner', 'manage-users')).toBe(true)
  })

  it('gives a session with no role nothing', () => {
    expect(may(undefined, 'talk')).toBe(false)
    expect(may(undefined, 'manage')).toBe(false)
  })

  it('gives a role it does not know nothing', () => {
    expect(may('guest' as never, 'manage')).toBe(false)
  })
})

describe('grants', () => {
  it('is adding anyone above a member', () => {
    expect(grants(undefined, 'member')).toBe(false)
    expect(grants(undefined, 'admin')).toBe(true)
    expect(grants(undefined, 'owner')).toBe(true)
  })

  it('is raising someone to admin or owner', () => {
    expect(grants('member', 'admin')).toBe(true)
    expect(grants('member', 'owner')).toBe(true)
    expect(grants('admin', 'owner')).toBe(true)
  })

  it('is not lowering anyone', () => {
    expect(grants('owner', 'admin')).toBe(false)
    expect(grants('owner', 'member')).toBe(false)
    expect(grants('admin', 'member')).toBe(false)
  })
})
