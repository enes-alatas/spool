import { describe, it, expect } from 'vitest'
import { channelRecipients, completeMention, mentionAt, mentionCompletions, mentionTokens } from './channel'
import type { LoopView } from './api'

const loop = (over: Partial<LoopView>): LoopView =>
  ({
    name: 'aster',
    status: 'active',
    in_fleet_channel: true,
    tg_bot_username: '',
    ...over,
  }) as LoopView

const fleet = [
  loop({ name: 'aster', tg_bot_username: 'aster_bot' }),
  loop({ name: 'briar', status: 'paused' }),
  loop({ name: 'cedar', in_fleet_channel: false }),
  loop({ name: 'dune', status: 'archived' }),
]

describe('mentionTokens', () => {
  it('reads mentions the way the server does', () => {
    expect(mentionTokens('@Aster and @briar, then @aster again')).toEqual(['aster', 'briar'])
  })

  // `me@work` is an address, not a mention; the boundary rule is the
  // server's (`mentionRe`), and a preview that disagreed with it would
  // promise a delivery the post does not make.
  it('needs a boundary before the @', () => {
    expect(mentionTokens('mail me@aster or @@briar')).toEqual([])
    expect(mentionTokens('(@aster)')).toEqual(['aster'])
  })
})

describe('channelRecipients', () => {
  it('reaches a named loop by its name or its bot', () => {
    expect(channelRecipients('@aster_bot look', fleet)).toEqual(['aster'])
    expect(channelRecipients('@ASTER look', fleet)).toEqual(['aster'])
  })

  // Named explicitly, a paused loop is still delivered to; outside the
  // channel or archived, nobody is (`inGroup`, ADR-0032 item 2).
  it('reaches only loops in the channel', () => {
    expect(channelRecipients('@briar @cedar @dune', fleet)).toEqual(['briar'])
  })

  // `@all` skips paused loops (`broadcastTargets`): a broadcast is not a
  // reason to wake one.
  it('broadcasts to the active loops in the channel', () => {
    expect(channelRecipients('@all standup', fleet)).toEqual(['aster'])
    expect(channelRecipients('@all and @briar', fleet)).toEqual(['aster', 'briar'])
  })

  it('reaches nobody when it names nobody', () => {
    expect(channelRecipients('morning, everyone', fleet)).toEqual([])
    expect(channelRecipients('@rana can you look', fleet)).toEqual([])
  })
})

describe('mentionAt', () => {
  it('finds the mention being typed at the caret', () => {
    expect(mentionAt('hi @as', 6)).toEqual({ start: 3, query: 'as' })
    expect(mentionAt('@', 1)).toEqual({ start: 0, query: '' })
  })

  it('finds nothing once the name is finished or when it is not a mention', () => {
    expect(mentionAt('hi @aster ', 10)).toBeNull()
    expect(mentionAt('me@as', 5)).toBeNull()
  })
})

describe('mentionCompletions', () => {
  it('offers loops in the channel, then @all', () => {
    expect(mentionCompletions('', fleet)).toEqual(['aster', 'briar', 'all'])
    expect(mentionCompletions('a', fleet)).toEqual(['aster', 'all'])
    expect(mentionCompletions('b', fleet)).toEqual(['briar'])
  })

  it('does not offer a loop a mention would not reach', () => {
    expect(mentionCompletions('c', fleet)).toEqual([])
    expect(mentionCompletions('d', fleet)).toEqual([])
  })
})

describe('completeMention', () => {
  it('replaces the partial name and puts the caret after it', () => {
    expect(completeMention('hi @as there', 3, 6, 'aster')).toEqual({ text: 'hi @aster  there', caret: 10 })
    expect(completeMention('@', 0, 1, 'all')).toEqual({ text: '@all ', caret: 5 })
  })
})
