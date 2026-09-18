import { describe, it, expect } from 'vitest'
import { fillTone, hasFillPct, formatTokens } from './format'

describe('hasFillPct', () => {
  // #122: a server older than `context_fill_pct` sends nothing, the field
  // arrives undefined, and the fleet row printed a percent sign with no
  // quantity in front of it. The guard cannot be falsiness — the server
  // computes the fill in integer arithmetic, so a loop holding ~1,500 tokens
  // of a 200k window truncates to a true, reported 0%.
  it('reports a measured zero', () => {
    expect(hasFillPct(0)).toBe(true)
  })

  it('does not report a field the server never sent', () => {
    expect(hasFillPct(undefined)).toBe(false)
  })
})

describe('fillTone', () => {
  // The tones are the configured thresholds, not the 75/90 they were once
  // fixed at: against the defaults of 40/70 (ADR-0022) a gauge at 45% is
  // already armed to rotate and has to say so.
  it('warms at the arm percent and heats at the force percent', () => {
    const t = { context_arm_percent: 40, context_force_percent: 70 }
    expect(fillTone(39, t)).toBe('')
    expect(fillTone(40, t)).toBe('warm')
    expect(fillTone(69, t)).toBe('warm')
    expect(fillTone(70, t)).toBe('hot')
  })

  it('falls back to the server defaults before settings load', () => {
    expect(fillTone(45)).toBe('warm')
    expect(fillTone(75)).toBe('hot')
  })
})

describe('formatTokens', () => {
  it('abbreviates at a thousand', () => {
    expect(formatTokens(999)).toBe('999')
    expect(formatTokens(1500)).toBe('1.5k')
  })
})
