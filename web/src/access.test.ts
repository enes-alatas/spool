import { describe, expect, it } from 'vitest'
import type { IgnoredSurface, LoopView, SlackSender, TGSender } from './api'
import { ignoredLine, legacySenders, pauseLine } from './access'

const tg = (over: Partial<TGSender>): TGSender => ({
  tg_user_id: 700000001,
  username: 'rana_example',
  display: 'Rana',
  status: 'allowed',
  user: '',
  pair_code: '',
  first_seen_via: '',
  created_at: 0,
  updated_at: 0,
  ...over,
})

const slack = (over: Partial<SlackSender>): SlackSender => ({
  slack_user_id: 'U0FIXTURE1',
  team_id: 'T0FIXTURE1',
  username: '',
  display: 'Devrim',
  status: 'allowed',
  user: '',
  pair_code: '',
  first_seen_via: '',
  created_at: 0,
  updated_at: 0,
  ...over,
})

const surface = (over: Partial<IgnoredSurface>): IgnoredSurface => ({
  surface: 'telegram',
  count: 0,
  ids: [],
  more: false,
  paused_until: 0,
  ...over,
})

describe('legacySenders', () => {
  it('keeps the senders no user holds, with the loops they own', () => {
    const loops = [
      { name: 'gardener', owner_tg_user_id: 700000001 },
      { name: 'courier', owner_slack_user_id: 'U0FIXTURE1' },
      { name: 'watcher' },
    ] as LoopView[]
    const senders = legacySenders(
      [tg({}), tg({ tg_user_id: 700000004, username: 'sam_example', user: 'sam' })],
      [slack({})],
      loops,
    )
    expect(senders.map((s) => [s.key, s.name, s.owns])).toEqual([
      ['telegram:700000001', '@rana_example', ['gardener']],
      ['slack:T0FIXTURE1/U0FIXTURE1', 'Devrim', ['courier']],
    ])
  })
})

describe('ignoredLine', () => {
  it('says nobody knocked when no surface counted anyone', () => {
    expect(ignoredLine({ window_hours: 24, surfaces: [surface({}), surface({ surface: 'slack' })] })).toBe(
      'No unknown sender messaged a bot in the last 24h.',
    )
  })

  it('names the ids of each surface that counted someone', () => {
    const line = ignoredLine({
      window_hours: 24,
      surfaces: [
        surface({ count: 2, ids: ['700000011', '700000012'] }),
        surface({ surface: 'slack', count: 1, ids: ['U0FIXTURE9'] }),
      ],
    })
    expect(line).toBe('3 unknown senders ignored in 24h: Telegram 700000011, 700000012 · Slack U0FIXTURE9.')
  })

  it('names five ids a surface and says how many more, and "more than" past the cap', () => {
    const ids = ['1', '2', '3', '4', '5', '6', '7']
    expect(ignoredLine({ window_hours: 24, surfaces: [surface({ count: 7, ids, more: true })] })).toBe(
      'More than 7 unknown senders ignored in 24h: Telegram 1, 2, 3, 4, 5 and 2 more.',
    )
  })

  it('says one sender in the singular', () => {
    expect(ignoredLine({ window_hours: 24, surfaces: [surface({ count: 1, ids: ['9'] })] })).toBe(
      '1 unknown sender ignored in 24h: Telegram 9.',
    )
  })
})

describe('pauseLine', () => {
  it('says which surface, until when, and that linked people are untouched', () => {
    const until = new Date(2026, 9, 14, 16, 52).getTime()
    expect(pauseLine('telegram', until)).toBe(
      'Linking on Telegram is paused until 16:52: strangers sent too many wrong codes. Anyone linked already is not affected.',
    )
  })
})
