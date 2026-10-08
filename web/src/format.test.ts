import { describe, it, expect } from 'vitest'
import {
  fillTone,
  hasFillPct,
  formatTokens,
  formatAgo,
  formatResets,
  formatUsd,
  inTurn,
  nextWake,
  gradedColor,
  spendShare,
  sumCostToday,
} from './format'

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

describe('sumCostToday', () => {
  // The header totals the real values, not the rounded ones on screen. Ten
  // loops at a third of a cent each read $0.00 on every row; the fleet still
  // spent three cents, and a total assembled from the displayed strings would
  // report nothing.
  it('sums before rounding, not after', () => {
    const loops = Array.from({ length: 10 }, () => ({ cost_today_usd: 0.003 }))
    expect(loops.every((l) => formatUsd(l.cost_today_usd) === '$0.00')).toBe(true)
    expect(formatUsd(sumCostToday(loops))).toBe('$0.03')
  })

  it('is zero for an empty fleet', () => {
    expect(formatUsd(sumCostToday([]))).toBe('$0.00')
  })
})

describe('spendShare', () => {
  // Four loops at a third of a cent each read $0.00 on every row, and each is
  // still a quarter of the day the fleet spent.
  it('divides the real values, not the rounded ones', () => {
    expect(spendShare(0.003, 0.012)).toBe(25)
  })

  it('rounds to a whole percent', () => {
    expect(spendShare(0.1382, 0.5464)).toBe(25)
    expect(spendShare(0.3025, 0.5464)).toBe(55)
  })

  // A blank column on a quiet day read as a missing one; 0% says the loops
  // have spent nothing of nothing.
  it('is zero on a day the fleet spent nothing', () => {
    expect(spendShare(0, 0)).toBe(0)
  })
})

describe('gradedColor', () => {
  it('runs from the muted ink through the active orange to the danger red', () => {
    expect(gradedColor(0)).toBe('color-mix(in oklab, var(--active) 0%, var(--text-muted))')
    expect(gradedColor(25)).toBe('color-mix(in oklab, var(--active) 50%, var(--text-muted))')
    expect(gradedColor(50)).toBe('color-mix(in oklab, var(--active) 100%, var(--text-muted))')
    expect(gradedColor(75)).toBe('color-mix(in oklab, var(--danger) 50%, var(--active))')
    expect(gradedColor(100)).toBe('color-mix(in oklab, var(--danger) 100%, var(--active))')
  })
})

describe('formatUsd', () => {
  // Cents always: a column of costs aligns, and a measured zero is a zero
  // rather than a blank.
  it('always shows cents', () => {
    expect(formatUsd(0)).toBe('$0.00')
    expect(formatUsd(12.5)).toBe('$12.50')
  })
})

describe('nextWake', () => {
  // A refused loop keeps its schedule entry, and the actor skips the tick.
  it('shows no wake while the model is refused', () => {
    expect(nextWake({ next_tick_at: 1000, model_refusal: 'refused' })).toBe(0)
    expect(nextWake({ next_tick_at: 1000, model_refusal: '' })).toBe(1000)
  })
})

describe('inTurn', () => {
  // #524: a fired tick leaves the fallback interval in next_tick_at until the
  // turn ends, so a waking or busy loop has no wake to count down to.
  it('holds while the loop wakes into a turn or runs it', () => {
    expect(inTurn('waking')).toBe(true)
    expect(inTurn('busy')).toBe(true)
  })

  // An idle or draining loop's turn is over, and its end set the next wake.
  it('does not hold between turns', () => {
    for (const state of ['idle', 'draining', 'asleep', 'paused', 'workstation_off', 'workstation_down']) {
      expect(inTurn(state)).toBe(false)
    }
  })
})

describe('formatResets', () => {
  const now = new Date(2026, 9, 8, 12, 0).getTime()

  it('counts down inside a day', () => {
    expect(formatResets(now + 41 * 60000, now)).toBe('resets in 41m')
    expect(formatResets(now + (2 * 60 + 14) * 60000, now)).toBe('resets in 2h 14m')
  })

  // A weekly window is planned around by its day, not by a 97-hour countdown.
  it('names the day and the clock past a day', () => {
    expect(formatResets(new Date(2026, 9, 12, 9, 0).getTime(), now)).toBe('resets Mon 09:00')
  })

  it('does not count below zero', () => {
    expect(formatResets(now - 60000, now)).toBe('resets in 0m')
  })
})

describe('formatAgo', () => {
  const now = new Date(2026, 9, 8, 12, 0).getTime()

  it('reads in the largest whole unit', () => {
    expect(formatAgo(now - 20000, now)).toBe('just now')
    expect(formatAgo(now - 3 * 60000, now)).toBe('3m ago')
    expect(formatAgo(now - 6 * 3600000, now)).toBe('6h ago')
    expect(formatAgo(now - 2 * 86400000, now)).toBe('2d ago')
  })
})
