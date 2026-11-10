import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import {
  channelLabel,
  channelOwner,
  channelRefusal,
  descriptionLength,
  loopList,
  loopsNotInTeam,
  loopsOutside,
  validChannelName,
  whereSaid,
} from './channels'

describe('validChannelName', () => {
  it('takes lowercase letters, digits and dashes', () => {
    expect(validChannelName('on-call')).toBe(true)
    expect(validChannelName('v2')).toBe(true)
    expect(validChannelName('9')).toBe(true)
  })

  it('refuses a leading dash, an empty name and one over 32', () => {
    expect(validChannelName('-ops')).toBe(false)
    expect(validChannelName('')).toBe(false)
    expect(validChannelName('a'.repeat(32))).toBe(true)
    expect(validChannelName('a'.repeat(33))).toBe(false)
  })

  it('refuses uppercase, an underscore, a space and a hash', () => {
    expect(validChannelName('Docs')).toBe(false)
    expect(validChannelName('owner_dm')).toBe(false)
    expect(validChannelName('on call')).toBe(false)
    expect(validChannelName('#docs')).toBe(false)
  })
})

describe('descriptionLength', () => {
  it('counts an emoji as one character, as the hub does', () => {
    expect(descriptionLength('ship it 🚀')).toBe(9)
  })
})

const fleet = [
  { name: 'watcher', team: 'release' },
  { name: 'archivist', team: '' },
  { name: 'gardener', team: 'docs' },
  { name: 'scribe', team: 'docs' },
]

describe('loopsOutside', () => {
  it('lists the loops not in a hub channel, sorted', () => {
    expect(loopsOutside({ loops: ['gardener'], team: '' }, fleet)).toEqual(['archivist', 'scribe', 'watcher'])
  })

  it("offers a team's channel only that team's loops", () => {
    expect(loopsOutside({ loops: ['gardener'], team: 'docs' }, fleet)).toEqual(['scribe'])
  })

  it('is empty when every loop is in', () => {
    expect(loopsOutside({ loops: ['gardener', 'scribe'], team: 'docs' }, fleet)).toEqual([])
  })
})

describe('loopsNotInTeam', () => {
  it("names the members outside the team, sorted, and a loop the list doesn't know", () => {
    expect(loopsNotInTeam(['watcher', 'gardener', 'archivist', 'gone'], 'docs', fleet)).toEqual([
      'archivist',
      'gone',
      'watcher',
    ])
  })

  it('is empty when a hub channel is given back to the hub', () => {
    expect(loopsNotInTeam(['archivist'], '', fleet)).toEqual([])
  })
})

describe('channelOwner', () => {
  it("names a team's channel by its team and any other as the hub's", () => {
    expect(channelOwner('docs')).toBe('team docs')
    expect(channelOwner('')).toBe('hub channel')
  })
})

describe('loopList', () => {
  it('reads as a sentence', () => {
    expect(loopList(['a'])).toBe('@a')
    expect(loopList(['a', 'b'])).toBe('@a and @b')
    expect(loopList(['a', 'b', 'c'])).toBe('@a, @b and @c')
  })
})

describe('channelRefusal', () => {
  const refused = (code: string) => new ApiError(409, `hub words for ${code}`, code)

  it('names the loop a team channel turned away', () => {
    expect(
      channelRefusal(refused('channel_in_other_team'), { channel: 'docs', team: 'docs', loop: 'watcher' }),
    ).toBe("#docs is team docs's channel, and @watcher isn't in team docs. Put it in the team first.")
  })

  it('names the loops that keep a channel from a team', () => {
    expect(
      channelRefusal(refused('channel_has_outsiders'), {
        channel: 'ops',
        team: 'docs',
        outsiders: ['archivist', 'watcher'],
      }),
    ).toBe(
      "@archivist and @watcher aren't in team docs. Take them out of #ops first, then give the channel to the team.",
    )
    expect(
      channelRefusal(refused('channel_has_outsiders'), {
        channel: 'ops',
        team: 'docs',
        outsiders: ['watcher'],
      }),
    ).toBe("@watcher isn't in team docs. Take it out of #ops first, then give the channel to the team.")
  })

  it("names the team a home channel can't leave", () => {
    expect(channelRefusal(refused('channel_is_home'), { channel: 'docs', team: '', home: 'docs' })).toBe(
      "#docs is team docs's home channel. Give the team another home channel, or none, first.",
    )
  })

  it("keeps the hub's words when it has nothing more to name, or for any other refusal", () => {
    expect(channelRefusal(refused('channel_has_outsiders'), { channel: 'ops', team: 'docs' })).toBe(
      'hub words for channel_has_outsiders',
    )
    expect(channelRefusal(refused('channel_exists'), { channel: 'ops', team: '' })).toBe(
      'hub words for channel_exists',
    )
    expect(channelRefusal(new Error('offline'), { channel: 'ops', team: '' })).toBe('offline')
  })
})

describe('whereSaid', () => {
  it('names a channel besides the fleet channel as a loop sends to it', () => {
    expect(whereSaid({ conversation: 'group', channel: 'docs', origin: 'loop' })).toBe('channel:docs')
  })

  it('keeps the fleet channel as group, with or without its name on the row', () => {
    expect(whereSaid({ conversation: 'group', channel: 'group', origin: 'loop' })).toBe('group')
    expect(whereSaid({ conversation: 'group', origin: 'telegram_group' })).toBe('group')
  })

  it('names a private conversation by its kind, and falls back to the origin', () => {
    expect(whereSaid({ conversation: 'owner_dm', origin: 'telegram_dm' })).toBe('owner_dm')
    expect(whereSaid({ conversation: '', origin: 'web' })).toBe('web')
  })
})

describe('channelLabel', () => {
  it('names the fleet channel by what it is, never its wire name', () => {
    expect(channelLabel('group')).toBe('fleet channel')
  })
  it('names any other channel with a hash', () => {
    expect(channelLabel('docs')).toBe('#docs')
  })
})
