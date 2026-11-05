import { describe, expect, it } from 'vitest'
import type { Room } from './api'
import { groupProgress } from './groupGuide'

const room = (room_id: string, channel = ''): Room => ({
  surface: 'telegram',
  room_id,
  title: room_id,
  channel,
  first_seen_at: 0,
  bound_at: 0,
})

describe('groupProgress', () => {
  it('waits on the first two steps until a group the steps did not know arrives', () => {
    const fleet = room('-1001', 'group')
    expect(groupProgress(new Set(), [])).toEqual({ arrived: undefined, needsChannel: false, done: 0 })
    expect(groupProgress(new Set(['-1001']), [fleet])).toEqual({
      arrived: undefined,
      needsChannel: true,
      done: 0,
    })
  })

  it('is done when the first group arrives carrying the fleet channel', () => {
    const fleet = room('-1001', 'group')
    expect(groupProgress(new Set(), [fleet])).toEqual({ arrived: fleet, needsChannel: false, done: 3 })
  })

  it('asks for a channel when a later group arrives unbound, then is done once it carries one', () => {
    const known = new Set(['-1001'])
    const fleet = room('-1001', 'group')
    const later = room('-1002')
    expect(groupProgress(known, [later, fleet])).toEqual({ arrived: later, needsChannel: true, done: 2 })
    const bound = room('-1002', 'docs')
    expect(groupProgress(known, [bound, fleet])).toEqual({ arrived: bound, needsChannel: true, done: 3 })
  })

  it('asks for a channel for any group that arrives unbound, fleet group or not', () => {
    const later = room('-1002')
    expect(groupProgress(new Set(['-1003']), [room('-1003'), later])).toEqual({
      arrived: later,
      needsChannel: true,
      done: 2,
    })
  })
})
