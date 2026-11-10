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
    expect(groupProgress(new Set(), [])).toEqual({ arrived: undefined, done: 0 })
    expect(groupProgress(new Set(['-1001']), [room('-1001', 'fleet')])).toEqual({
      arrived: undefined,
      done: 0,
    })
  })

  // ADR-0068: no group binds itself, a loop's first included, so the
  // operator always picks the channel it carries.
  it('asks for a channel when the first group arrives unbound, then is done once it carries one', () => {
    const first = room('-1001')
    expect(groupProgress(new Set(), [first])).toEqual({ arrived: first, done: 2 })
    const bound = room('-1001', 'handbook')
    expect(groupProgress(new Set(), [bound])).toEqual({ arrived: bound, done: 3 })
  })

  it('asks the same for a later group, whatever the loop already has', () => {
    const known = new Set(['-1001'])
    const later = room('-1002')
    expect(groupProgress(known, [later, room('-1001', 'fleet')])).toEqual({ arrived: later, done: 2 })
    expect(groupProgress(known, [later, room('-1001')])).toEqual({ arrived: later, done: 2 })
  })
})
