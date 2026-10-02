import { describe, it, expect } from 'vitest'
import type { Reaction } from './api'
import { reactionChips } from './reactions'

let next = 0
const reaction = (emoji: string, reactor: string): Reaction => ({
  id: ++next,
  message_id: 1,
  reactor_key: `loop:${reactor}`,
  reactor,
  emoji,
  ts: next,
})

describe('reactionChips', () => {
  it('is one chip per emoji, with everyone who put it there', () => {
    expect(reactionChips([reaction('👍', 'terra'), reaction('👀', 'quinn'), reaction('👍', 'enes')])).toEqual(
      [
        { emoji: '👍', reactors: ['terra', 'enes'] },
        { emoji: '👀', reactors: ['quinn'] },
      ],
    )
  })

  it('keeps a custom emoji with no Unicode as its name', () => {
    expect(reactionChips([reaction(':shipit:', 'milo')])).toEqual([{ emoji: ':shipit:', reactors: ['milo'] }])
  })

  it('is nothing for no reactions', () => {
    expect(reactionChips([])).toEqual([])
  })
})
