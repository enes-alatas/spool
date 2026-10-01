import { describe, expect, it } from 'vitest'
import { descriptionLength, loopsOutside, validChannelName } from './channels'

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

describe('loopsOutside', () => {
  it('lists the loops not in the channel, sorted', () => {
    expect(loopsOutside(['gardener'], ['watcher', 'archivist', 'gardener'])).toEqual(['archivist', 'watcher'])
  })

  it('is empty when every loop is in', () => {
    expect(loopsOutside(['a', 'b'], ['b', 'a'])).toEqual([])
  })
})
