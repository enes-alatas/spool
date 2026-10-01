import { describe, expect, it } from 'vitest'
import { checkPairCode, pairInput } from './pairing'

describe('pairInput', () => {
  it('keeps a code read out in lowercase or with a space', () => {
    expect(pairInput('r4t z8p', 'R4TZ8P')).toBe('R4TZ8P')
  })

  it('stops at the length of the code', () => {
    expect(pairInput('R4TZ8PQ', 'R4TZ8P')).toBe('R4TZ8P')
  })

  it('drops what cannot be part of a code', () => {
    expect(pairInput('R4-TZ.8P', 'R4TZ8P')).toBe('R4TZ8P')
  })
})

describe('checkPairCode', () => {
  it('is empty before anything is typed', () => {
    expect(checkPairCode('', 'R4TZ8P')).toBe('empty')
  })

  it('is partial while a prefix is being typed, right or not', () => {
    expect(checkPairCode('R4T', 'R4TZ8P')).toBe('partial')
    expect(checkPairCode('XXX', 'R4TZ8P')).toBe('partial')
  })

  it('matches the code exactly', () => {
    expect(checkPairCode('R4TZ8P', 'R4TZ8P')).toBe('match')
  })

  it('is wrong once a full-length code differs', () => {
    expect(checkPairCode('R4TZ8Q', 'R4TZ8P')).toBe('wrong')
  })
})
