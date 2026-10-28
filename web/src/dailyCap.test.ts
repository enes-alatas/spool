import { describe, expect, it } from 'vitest'
import {
  cappedBy,
  cappedNote,
  dailyCapField,
  dailyCapHint,
  dailyCapValue,
  isPreset,
  presetLabel,
  thenCappedNote,
} from './dailyCap'

describe('presets', () => {
  it('names no cap in words and the rest in dollars', () => {
    expect(presetLabel(0)).toBe('no cap')
    expect(presetLabel(10)).toBe('$10')
  })

  it('takes an amount no chip names as custom', () => {
    expect(isPreset(undefined)).toBe(true)
    expect(isPreset(25)).toBe(true)
    expect(isPreset(7.5)).toBe(false)
  })
})

describe('dailyCapValue', () => {
  it('reads an empty field as no cap', () => {
    expect(dailyCapValue('')).toBe(0)
    expect(dailyCapValue('  ')).toBe(0)
  })

  it('reads dollars and cents, with or without the sign', () => {
    expect(dailyCapValue('5')).toBe(5)
    expect(dailyCapValue('2.50')).toBe(2.5)
    expect(dailyCapValue(' $12 ')).toBe(12)
  })

  it('refuses what is not an amount', () => {
    expect(dailyCapValue('five')).toBeUndefined()
    expect(dailyCapValue('-3')).toBeUndefined()
    expect(dailyCapValue('1.234')).toBeUndefined()
    expect(dailyCapValue('1e3')).toBeUndefined()
  })
})

describe('dailyCapField', () => {
  it('shows no cap as an empty field', () => {
    expect(dailyCapField(0)).toBe('')
    expect(dailyCapField(undefined)).toBe('')
    expect(dailyCapField(2.5)).toBe('2.5')
  })
})

describe('dailyCapHint', () => {
  it('says what the cap does', () => {
    expect(dailyCapHint(false)).toMatch(
      /sleeps until midnight on the hub's clock; raising the cap past today's spend, or removing it, wakes it/,
    )
    expect(dailyCapHint(false)).not.toMatch(/estimate/)
  })

  it('says a cloud loop is capped on the estimate', () => {
    expect(dailyCapHint(true)).toMatch(/the cap counts the estimate/)
  })
})

describe('cappedNote', () => {
  it('keeps the plan cap note for a loop under the plan cap alone', () => {
    expect(cappedNote({ daily_cap_usd: 5 }, true)).toBe(
      'Capped: the plan cap holds it until the window resets. Messages wait in its inbox.',
    )
  })

  it('names the daily cap and what lifts it, not Resume now', () => {
    const note = cappedNote({ daily_cap_usd: 5, daily_cap_reached: true }, false)
    expect(note).toBe(
      "Capped: it reached its $5.00 daily cap. It sleeps until midnight on the hub's clock, or wakes now if you raise the cap past today's spend or remove it. Messages wait in its inbox.",
    )
    expect(note).not.toMatch(/Resume/)
  })

  it('says both caps must lift when both hold', () => {
    expect(cappedNote({ daily_cap_usd: 5, daily_cap_reached: true }, true)).toMatch(
      /reached its \$5\.00 daily cap, and its login.s plan cap holds it\. It wakes when both lift/,
    )
  })
})

describe('thenCappedNote', () => {
  it('names the daily cap ahead of the plan cap', () => {
    expect(thenCappedNote(true, true)).toMatch(/reached its daily cap/)
    expect(thenCappedNote(false, true)).toBe('Then capped: it sleeps when this turn ends.')
    expect(thenCappedNote(false, false)).toBeUndefined()
  })
})

describe('cappedBy', () => {
  it('says which cap a capped row is under', () => {
    expect(cappedBy({ daily_cap_reached: true }, false)).toBe('Capped by its daily cap')
    expect(cappedBy({ daily_cap_reached: true }, true)).toBe('Capped by its daily cap and the plan cap')
    expect(cappedBy({}, true)).toBe('Capped by the plan cap')
  })
})
