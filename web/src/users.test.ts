import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { confirmText, refreshAfterUserChange } from './users'

describe('confirmText', () => {
  it('names the user whose row it is', () => {
    expect(confirmText('sam', false, { kind: 'remove' })).toMatch(/^Remove sam\? Their sessions end now/)
    expect(confirmText('sam', false, { kind: 'role', to: 'admin' })).toMatch(/^Make sam an admin\?/)
  })

  it('speaks to the owner on their own row', () => {
    expect(confirmText('admin', true, { kind: 'remove' })).toMatch(/^Remove yourself\? You're signed out now/)
    expect(confirmText('admin', true, { kind: 'role', to: 'member' })).toMatch(/^Make yourself a member\?/)
  })
})

describe('refreshAfterUserChange', () => {
  it('reloads the session the room gates on, not only the list', () => {
    const qc = new QueryClient()
    for (const key of ['users', 'session', 'me']) qc.setQueryData([key], {})
    refreshAfterUserChange(qc)
    for (const key of ['users', 'session', 'me']) {
      expect(qc.getQueryState([key])?.isInvalidated, key).toBe(true)
    }
  })
})
