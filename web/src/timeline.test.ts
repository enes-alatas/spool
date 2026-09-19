import { describe, it, expect } from 'vitest'
import { toEntries } from './timeline'
import type { LoopEvent } from './api'

let nextId = 0

function ev(type: string, session: string, payload: unknown, subtype = ''): LoopEvent {
  nextId += 1
  return {
    id: nextId,
    loop_id: 'iris',
    session_id: session,
    turn_id: `t${nextId}`,
    ts: nextId,
    type,
    subtype,
    payload: JSON.stringify(payload),
  }
}

const result = (session: string, total: number, isError = false) =>
  ev('result', session, { total_cost_usd: total, duration_ms: 5, is_error: isError })

// A fresh spawn is stored under the id Spool minted for the session, and the
// CLI's init line may then replace it (`handleEvent` in internal/loop),
// leaving the results that follow under a different id from the spawn that
// opened them. The fixture spawns under 'minted' for that reason: keying the
// fresh-start allowance to the spawn's own session would find nothing.
const spawn = (resume: boolean, session = 'minted') => ev('spool', session, { pid: 1, resume }, 'proc_spawn')

// Costs are compared at the four decimals the footer prints: a difference of
// two floats carries a binary remainder (9.37 - 9.08 is 0.28999999999999915)
// that no reader can see and no assertion should turn on.
const costs = (events: LoopEvent[], hole?: { from: number; to: number }) =>
  toEntries(events, hole)
    .filter((e) => e.kind === 'result')
    .map((e) => (e.kind === 'result' ? [Number(e.costUsd.toFixed(4)), e.costBasis] : []))

describe('toEntries result costs', () => {
  // #195: the CLI's total_cost_usd is the session's running total under
  // --resume, so three twelve-cent turns used to read 0.12 / 0.24 / 0.37.
  it('prices each turn at its increment over the session total', () => {
    expect(costs([spawn(false), result('s1', 0.121), result('s1', 0.248), result('s1', 0.369)])).toEqual([
      [0.121, 'turn'],
      [0.127, 'turn'],
      [0.121, 'turn'],
    ])
  })

  // A total that did not grow means the turn spent nothing — an errored turn
  // repeats the previous figure — never that the loop earned money back.
  it('never reports a negative cost', () => {
    expect(costs([spawn(false), result('s1', 0.5), result('s1', 0.5, true)])).toEqual([
      [0.5, 'turn'],
      [0, 'turn'],
    ])
  })

  // Each session bills from zero, and sessions interleave in the window only
  // in the sense that a new one follows an old one; the old one's total must
  // not become the new one's baseline.
  it('starts a new session from zero', () => {
    expect(costs([spawn(false), result('s1', 9.08), spawn(false), result('s2', 0.29)])).toEqual([
      [9.08, 'turn'],
      [0.29, 'turn'],
    ])
  })

  // The window opens on the newest events: the oldest turn loaded for a
  // resumed session has no baseline in it. Reporting the running total and
  // saying so beats guessing a baseline that would misprice the turn.
  it('marks a turn whose session began before the window', () => {
    expect(costs([result('s1', 9.08), result('s1', 9.37)])).toEqual([
      [9.08, 'session'],
      [0.29, 'turn'],
    ])
  })

  // A resumed spawn does not open a session at zero — the total it carries on
  // reporting includes everything spent before the restart.
  it('does not treat a resumed spawn as a fresh session', () => {
    expect(costs([spawn(true), result('s1', 9.08), result('s1', 9.37)])).toEqual([
      [9.08, 'session'],
      [0.29, 'turn'],
    ])
  })

  // The fresh-spawn allowance belongs to the session that spawn opened, not
  // to whichever session reports next: after it is spent the window is back
  // to having no baseline.
  it('spends the fresh-spawn allowance on one session only', () => {
    expect(costs([spawn(false), result('s1', 0.4), result('s2', 5.0), result('s2', 5.5)])).toEqual([
      [0.4, 'turn'],
      [5.0, 'session'],
      [0.5, 'turn'],
    ])
  })
})

// A hole is the stretch the window jumped over: events the page was never
// handed, between two it holds (LoopDetail's `record.hole`). Evidence read
// across one is evidence about turns that are missing, not about this turn.
describe('toEntries result costs across a hole', () => {
  it('does not spend the fresh-spawn allowance across the hole', () => {
    const opened = spawn(false)
    const turn = result('s1', 0.37)
    // Without the hole this is the session's first turn and 0.37 is its own
    // cost; with it, turns fell between the spawn and this one and 0.37 is
    // what they and this one cost together.
    expect(costs([opened, turn], { from: opened.id, to: turn.id })).toEqual([[0.37, 'session']])
  })

  it('does not subtract a baseline read across the hole', () => {
    const before = result('s1', 1.0)
    const after = result('s1', 9.0)
    expect(costs([before, after], { from: before.id, to: after.id })).toEqual([
      [1.0, 'session'],
      [9.0, 'session'],
    ])
  })

  it('trusts a baseline that lies on its own side of the hole', () => {
    const opened = spawn(false)
    const first = result('s1', 0.4)
    const second = result('s1', 0.9)
    expect(costs([opened, first, second], { from: second.id, to: second.id + 100 })).toEqual([
      [0.4, 'turn'],
      [0.5, 'turn'],
    ])
  })
})
