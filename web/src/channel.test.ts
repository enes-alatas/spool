import { describe, it, expect } from 'vitest'
import {
  channelRecipients,
  completeMention,
  inChannelOf,
  mentionAt,
  mentionCompletions,
  mentionTokens,
} from './channel'
import type { LoopView } from './api'

const loop = (over: Partial<LoopView>): LoopView =>
  ({
    name: 'aster',
    status: 'active',
    tg_bot_username: '',
    ...over,
  }) as LoopView

const fleet = [
  loop({ id: 'l-aster', name: 'aster', tg_bot_username: 'aster_bot' }),
  loop({ id: 'l-briar', name: 'briar', status: 'paused' }),
  loop({ id: 'l-cedar', name: 'cedar' }),
  loop({ id: 'l-dune', name: 'dune', status: 'archived' }),
]

// The channel the posts are for: every loop but cedar.
const inIt = inChannelOf(['aster', 'briar', 'dune'])

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
    expect(channelRecipients('@aster_bot look', fleet, undefined, inIt)).toEqual(['aster'])
    expect(channelRecipients('@ASTER look', fleet, undefined, inIt)).toEqual(['aster'])
  })

  // Named explicitly, a paused loop is still delivered to; outside the
  // channel or archived, nobody is (`inGroup`, ADR-0032).
  it('reaches only loops in the channel', () => {
    expect(channelRecipients('@briar @cedar @dune', fleet, undefined, inIt)).toEqual(['briar'])
  })

  // `@all` skips paused loops (`broadcastTargets`): a broadcast is not a
  // reason to wake one.
  it('broadcasts to the active loops in the channel', () => {
    expect(channelRecipients('@all standup', fleet, undefined, inIt)).toEqual(['aster'])
    expect(channelRecipients('@all and @briar', fleet, undefined, inIt)).toEqual(['aster', 'briar'])
  })

  it('reaches nobody when it names nobody', () => {
    expect(channelRecipients('morning, everyone', fleet, undefined, inIt)).toEqual([])
    expect(channelRecipients('@rana can you look', fleet, undefined, inIt)).toEqual([])
  })
  // ADR-0025: a reply addresses what it answers, so answering a loop needs
  // no mention — the router adds the replied-to author (#311).
  it('reaches the loop a reply answers without a mention', () => {
    expect(channelRecipients('which change?', fleet, { from_loop_id: 'l-briar' }, inIt)).toEqual(['briar'])
    expect(channelRecipients('@aster too', fleet, { from_loop_id: 'l-briar' }, inIt)).toEqual([
      'aster',
      'briar',
    ])
  })

  // The replied-to loop is still held to `inGroup`: a reply to a loop that
  // has since left the channel, or been archived, reaches nobody.
  it('does not reach a replied-to loop outside the channel', () => {
    expect(channelRecipients('still there?', fleet, { from_loop_id: 'l-cedar' }, inIt)).toEqual([])
    expect(channelRecipients('still there?', fleet, { from_loop_id: 'l-dune' }, inIt)).toEqual([])
  })

  // A person's post has no loop author; answering it adds nobody, and a
  // missing field (an operator's or a person's post) reads the same.
  it('adds nobody for a reply to a person', () => {
    expect(channelRecipients('thanks', fleet, {}, inIt)).toEqual([])
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
    expect(mentionCompletions('', fleet, inIt)).toEqual(['aster', 'briar', 'all'])
    expect(mentionCompletions('a', fleet, inIt)).toEqual(['aster', 'all'])
    expect(mentionCompletions('b', fleet, inIt)).toEqual(['briar'])
  })

  it('does not offer a loop a mention would not reach', () => {
    expect(mentionCompletions('c', fleet, inIt)).toEqual([])
    expect(mentionCompletions('d', fleet, inIt)).toEqual([])
  })
})

describe('completeMention', () => {
  it('replaces the partial name and puts the caret after it', () => {
    expect(completeMention('hi @as there', 3, 6, 'aster')).toEqual({ text: 'hi @aster  there', caret: 10 })
    expect(completeMention('@', 0, 1, 'all')).toEqual({ text: '@all ', caret: 5 })
  })
})

describe('another channel', () => {
  // aster is in both; briar is in the first channel but not docs, and cedar
  // the other way round, so each channel's members are told apart.
  const docs = inChannelOf(['aster', 'cedar'])

  it('reaches and offers its own loops, whatever other channels they are in', () => {
    expect(channelRecipients('@aster @briar @cedar hi', fleet, undefined, docs)).toEqual(['aster', 'cedar'])
    expect(mentionCompletions('', fleet, docs)).toEqual(['aster', 'cedar', 'all'])
  })
})
