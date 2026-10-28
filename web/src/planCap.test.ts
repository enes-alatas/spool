import { describe, expect, it } from 'vitest'
import type { PlanUsage } from './api'
import {
  capFields,
  capGate,
  capIdleStatus,
  cappedLogins,
  capWake,
  capReason,
  capValue,
  loginUsage,
  loopPlanCapped,
  ringName,
  usageLogins,
} from './planCap'

const now = Date.UTC(2026, 9, 9, 12, 0)
const minutes = (m: number) => now + m * 60000

const usage = (over: Partial<PlanUsage> = {}): PlanUsage => ({
  five_hour: { used_percent: 93.4, resets_at: minutes(41), cap_percent: 90 },
  seven_day: { used_percent: 72, resets_at: minutes(3 * 24 * 60), cap_percent: 90 },
  as_of: minutes(-3),
  ...over,
})

describe('capReason', () => {
  it('names the window over its threshold and when it resets', () => {
    expect(capReason(usage({ cap: { windows: ['five_hour'], until: minutes(41) } }), now)).toBe(
      'The 5-hour window is at 93%, over its 90% threshold. Loops sleep until it resets in 41m; messages wait in their inboxes.',
    )
  })

  it('waits for the later reset when both windows are over', () => {
    const both = usage({
      seven_day: { used_percent: 95, resets_at: minutes(90), cap_percent: 90 },
      cap: { windows: ['five_hour', 'seven_day'], until: minutes(90) },
    })
    expect(capReason(both, now)).toBe(
      'The 5-hour window is at 93%, over its 90% threshold and the 7-day window is at 95%, over its 90% threshold. Loops sleep until the later one resets in 1h 30m; messages wait in their inboxes.',
    )
  })

  it('says nothing while nothing is capped', () => {
    expect(capReason(usage(), now)).toBeUndefined()
    expect(capReason(usage({ cap: null }), now)).toBeUndefined()
    expect(capReason(undefined, now)).toBeUndefined()
  })
})

describe('capReason for one login of several', () => {
  const capped = usage({ cap: { windows: ['five_hour'], until: minutes(41) } })
  it('names the loops on the login that sleep', () => {
    expect(capReason(capped, now, ['scout', 'gardener', 'scribe'])).toBe(
      'The 5-hour window is at 93%, over its 90% threshold. scout, gardener and scribe sleep until it resets in 41m; messages wait in their inboxes.',
    )
    expect(capReason(capped, now, ['scout'])).toContain('. scout sleeps until it resets in 41m;')
  })

  it('says alone which loops sleep and when they wake, for a ring', () => {
    expect(capWake(capped.cap!, now, ['scout', 'gardener'])).toBe(
      'scout and gardener sleep until it resets in 41m; messages wait in their inboxes.',
    )
  })

  it('says when no loop runs on the login', () => {
    expect(capReason(capped, now, [])).toContain('. No loop runs on it; one moved to it sleeps until')
  })
})

describe("a loop's login cap", () => {
  const cap = { windows: ['five_hour' as const], until: minutes(41) }
  const several = usage({
    logins: [
      { name: 'default', ...usage() },
      { name: 'ada', ...usage({ cap }) },
    ],
  })

  it("reads the loop's own login, the default for a bare or cloud loop", () => {
    expect(loopPlanCapped(several, { claude_login: 'ada' })).toBe(true)
    expect(loopPlanCapped(several, { claude_login: '' })).toBe(false)
    expect(loopPlanCapped(several, { claude_login: 'robin' })).toBe(false)
  })

  it('reads the top level as the default on a hub older than #1073', () => {
    const old = usage({ cap })
    expect(loginUsage(old, 'default')).toBe(old)
    expect(loopPlanCapped(old, { claude_login: '' })).toBe(true)
    expect(loopPlanCapped(old, { claude_login: 'ada' })).toBe(false)
  })

  it('lists the capped logins of several, and the one of a hub with one', () => {
    expect(cappedLogins(several).map((login) => login.name)).toEqual(['ada'])
    expect(cappedLogins(usage())).toEqual([])
    expect(cappedLogins(usage({ cap })).map((login) => login.name)).toEqual(['default'])
    const one = usage({ cap, logins: [{ name: 'default', ...usage({ cap }) }] })
    expect(cappedLogins(one).map((login) => login.name)).toEqual(['default'])
  })
})

describe('capIdleStatus', () => {
  it('says where usage stands', () => {
    expect(capIdleStatus(usage(), now)).toBe('Not capped: 5-hour at 93%, 7-day at 72%.')
  })

  it('says a Resume now is holding, until when', () => {
    expect(capIdleStatus(usage({ resumed_until: minutes(41) }), now)).toBe(
      'Resumed: the cap holds off until the window resets in 41m.',
    )
    // a hold whose reset has passed is no hold
    expect(capIdleStatus(usage({ resumed_until: minutes(-1) }), now)).toMatch(/^Not capped/)
  })

  it('says the cap cannot fire on unknown usage, which is how it fails open', () => {
    expect(capIdleStatus({ five_hour: null, seven_day: null, unknown: 'no loop has reported' }, now)).toBe(
      'Usage is unknown, so the cap cannot fire.',
    )
  })

  it('says nothing while the read is loading, rather than calling usage unknown', () => {
    expect(capIdleStatus(undefined, now)).toBeUndefined()
  })
})

describe('the threshold fields', () => {
  it('show an off cap as an empty field', () => {
    expect(capFields(90, 0)).toEqual({ fiveHour: '90', sevenDay: '' })
  })

  it('send an empty field as 0, which is off, and refuse what is not a whole number', () => {
    expect(capValue('')).toBe(0)
    expect(capValue(' 85 ')).toBe(85)
    expect(capValue('8.5')).toBeUndefined()
    expect(capValue('ninety')).toBeUndefined()
  })

  it('save only a change the server can be sent', () => {
    const stored = capFields(90, 90)
    expect(capGate(stored, null).sendable).toBe(false)
    expect(capGate(stored, { fiveHour: '90', sevenDay: '90' }).changed).toBe(false)
    expect(capGate(stored, { fiveHour: '', sevenDay: '90' }).sendable).toBe(true)
    expect(capGate(stored, { fiveHour: '80%', sevenDay: '90' }).sendable).toBe(false)
  })
})

describe('usageLogins', () => {
  const line = (name: string) => ({ ...usage(), name })
  it('gives a ring a login on a hub with more than one', () => {
    const logins = [line('default'), line('ada')]
    expect(usageLogins(usage({ logins }))).toEqual(logins)
  })
  it('leaves the strip as it was with one login, or on a hub that lists none', () => {
    expect(usageLogins(usage({ logins: [line('default')] }))).toBeUndefined()
    expect(usageLogins(usage())).toBeUndefined()
    expect(usageLogins(undefined)).toBeUndefined()
  })
})

describe('ringName', () => {
  it('keeps a name that fits the ring and cuts a longer one short', () => {
    expect(ringName('default')).toBe('default')
    expect(ringName('research')).toBe('research')
    expect(ringName('research-team')).toBe('researc…')
  })
})
