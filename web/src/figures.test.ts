import { describe, expect, it } from 'vitest'
import type { Figures, LoopFigures } from './api'
import {
  LANGFUSE_DOCS,
  TOOLS_SHOWN,
  allOff,
  cardLine,
  count,
  figuresRefetch,
  turnAt,
  figuresAge,
  figuresKey,
  figuresLine,
  figuresWindow,
  formatDuration,
  perTurn,
  toolMix,
  turnShares,
} from './figures'

const now = 1762520400000
const figures: Figures = {
  turns: 42,
  failed_turns: 2,
  cost_usd: 3.36,
  tokens: { input: 51240, output: 18480, cache_read: 2310000, cache_write: 96000 },
  turn_duration_ms: { p50: 41000, p95: 182000, max: 420000 },
  failures: [{ class: 'rate_limit', count: 2 }],
  tools: [
    { name: 'mcp__spool__send_message', calls: 40, failures: 0, duration_ms_p50: 120 },
    { name: 'Bash', calls: 120, failures: 3, duration_ms_p50: 900 },
  ],
  per_turn: [],
}
const served = (over: Partial<LoopFigures>): LoopFigures => ({
  loop: 'terra',
  window: '24h',
  langfuse: 'ok',
  fetched_at: now - 2 * 60000,
  refresh_sec: 300,
  figures,
  ...over,
})

describe('the figures window', () => {
  it('is one the route takes, else the default', () => {
    expect(figuresWindow('7d')).toBe('7d')
    expect(figuresWindow('2d')).toBe('24h')
    expect(figuresWindow(null)).toBe('24h')
  })
  it('asks the hub again when another is picked', () => {
    expect(figuresKey('terra', '7d')).not.toEqual(figuresKey('terra', '24h'))
    expect(figuresKey('terra', '7d')).toEqual(['figures', 'terra', '7d'])
  })
})

describe('a loop with figures', () => {
  it('reads its spend and tokens per turn', () => {
    expect(perTurn(figures)).toEqual({ cost: 0.08, input: 1220, output: 440, cacheRead: 55000 })
    expect(perTurn({ ...figures, turns: 0, cost_usd: 0 }).cost).toBe(0)
  })
  it('reads its turn durations at a glance', () => {
    expect(formatDuration(900)).toBe('900ms')
    expect(formatDuration(41000)).toBe('41s')
    expect(formatDuration(182000)).toBe('3m 2s')
    expect(formatDuration(420000)).toBe('7m')
    expect(formatDuration(3840000)).toBe('1h 4m')
  })
  it('lists its tools by calls, folding the tail into one row', () => {
    expect(toolMix(figures.tools).map((t) => [t.name, t.share])).toEqual([
      ['Bash', 1],
      ['mcp__spool__send_message', 40 / 120],
    ])
    const many = Array.from({ length: TOOLS_SHOWN + 2 }, (_, i) => ({
      name: `t${i}`,
      calls: 100 - i,
      failures: i === TOOLS_SHOWN + 1 ? 1 : 0,
      duration_ms_p50: 1,
    }))
    const rows = toolMix(many)
    expect(rows).toHaveLength(TOOLS_SHOWN + 1)
    expect(rows.at(-1)).toMatchObject({ name: '2 other tools', calls: 94 + 93, failures: 1, other: 2 })
  })
  it("draws each turn's spend against the window's most", () => {
    const turn = { started_at: 0, duration_ms: 1, input_tokens: 0, output_tokens: 0 }
    const rest = { cache_read_tokens: 0, cache_write_tokens: 0, tool_calls: 0, failed: false }
    expect(turnShares([0.1, 0.4, 0.2].map((c) => ({ ...turn, ...rest, cost_usd: c })))).toEqual([
      0.25, 1, 0.5,
    ])
    expect(turnShares([{ ...turn, ...rest, cost_usd: 0 }])).toEqual([0])
  })
  it('shows them and says how old they are', () => {
    expect(figuresLine(served({}))).toBeNull()
    expect(figuresAge(served({}), now)).toEqual({ text: 'from Langfuse · 2m ago', stale: false })
  })
})

describe('figures Langfuse could not refresh', () => {
  it('still show, and say how old they are as a warning', () => {
    const stale = served({ langfuse: 'stale', fetched_at: now - 3 * 3600000 })
    expect(figuresLine(stale)).toBeNull()
    expect(figuresAge(stale, now)).toEqual({
      text: "Langfuse isn't answering: these are from 3h ago",
      stale: true,
    })
  })
})

describe('a loop without figures', () => {
  it('says plainly why, with no age', () => {
    const off = served({ langfuse: 'off', figures: null, fetched_at: 0 })
    expect(figuresLine(off)).toBe('Traces are off, so there are no figures to show.')
    expect(figuresAge(off, now)).toBeNull()
    expect(figuresLine(served({ langfuse: 'pending', figures: null, fetched_at: 0 }))).toMatch(
      /^Traces are on/,
    )
    expect(figuresLine(served({ langfuse: 'unsupported', figures: null }))).toMatch(/^A cloud loop/)
  })
  it('points to the operations docs, not Settings', () => {
    expect(LANGFUSE_DOCS).toMatch(/docs\/OPERATIONS\.md#langfuse-beside-the-hub$/)
  })
})

describe('the overview', () => {
  it('gives each loop a card, with a short line where it has no figures', () => {
    expect(cardLine(served({}))).toBeNull()
    expect(cardLine(served({ langfuse: 'stale' }))).toBeNull()
    expect(cardLine(served({ langfuse: 'unsupported', figures: null }))).toBe('a cloud loop sends no traces')
    expect(cardLine(served({ langfuse: 'pending', figures: null }))).toMatch(/^traces on/)
  })
  it('says once that traces are off, when they are for every loop', () => {
    const off = served({ langfuse: 'off', figures: null, fetched_at: 0 })
    expect(allOff([off, { ...off, loop: 'iris' }])).toBe(true)
    expect(allOff([off, served({ langfuse: 'unsupported', figures: null })])).toBe(true)
    expect(allOff([off, served({})])).toBe(false)
    expect(allOff([served({ langfuse: 'unsupported', figures: null })])).toBe(false)
    expect(allOff([])).toBe(false)
  })
})

describe('an open page', () => {
  it("asks again at the hub's refresh period, and not without an answer", () => {
    expect(figuresRefetch(served({ langfuse: 'pending', figures: null, fetched_at: 0 }))).toBe(300000)
    expect(figuresRefetch(served({ refresh_sec: 1 }))).toBe(15000)
    expect(figuresRefetch(undefined)).toBe(false)
  })
  it('reads the turn under the pointer, or the newest once the turns shorten', () => {
    expect(turnAt(38, null)).toBe(37)
    expect(turnAt(38, 4)).toBe(4)
    expect(turnAt(12, 30)).toBe(11)
  })
  it('counts one turn as one', () => {
    expect(count(1, 'turn')).toBe('1 turn')
    expect(count(38, 'turn')).toBe('38 turns')
    expect(count(1, 'tool call')).toBe('1 tool call')
  })
})
