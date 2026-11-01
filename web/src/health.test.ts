import { describe, expect, it } from 'vitest'
import {
  errorTurnsField,
  errorTurnsValue,
  healthNote,
  openFor,
  problemHealth,
  problemLine,
  problemMeaning,
  problemNext,
  turnMinutesField,
  turnMinutesValue,
} from './health'

const minute = 60 * 1000
const now = 100_000 * minute

describe('openFor', () => {
  it('says how long, in the largest whole unit', () => {
    expect(openFor(now - 30 * 1000, now)).toBe('under a minute')
    expect(openFor(now - 40 * minute, now)).toBe('40m')
    expect(openFor(now - 125 * minute, now)).toBe('2h')
    expect(openFor(now - 3 * 24 * 60 * minute, now)).toBe('3d')
  })
})

describe('problemLine', () => {
  it('names the symptom and how long it has been open', () => {
    expect(problemLine({ symptom: 'workstation_down', since: now - 125 * minute }, now)).toBe(
      'Workstation down for 2h',
    )
    expect(problemLine({ symptom: 'turn_errors', since: now - 40 * minute }, now)).toBe(
      'Turns failing for 40m',
    )
  })

  it('says a symptom it does not know by its name', () => {
    expect(problemLine({ symptom: 'hanging_wake', since: now - 5 * minute }, now)).toBe('hanging_wake for 5m')
  })
})

describe('healthNote', () => {
  it('lists every open problem on one line', () => {
    const problems = [
      { symptom: 'workstation_down', since: now - 125 * minute },
      { symptom: 'turn_errors', since: now - 40 * minute },
    ]
    expect(healthNote(problems, now)).toBe('workstation down for 2h · turns failing for 40m')
  })
})

describe('problemHealth', () => {
  it('makes a workstation down the alarm and failing turns a warning', () => {
    expect(problemHealth({ symptom: 'workstation_down', since: 0 })).toBe('failing')
    // a cloud loop held by its public address does no turns either (#864)
    expect(problemHealth({ symptom: 'cloud_address', since: 0 })).toBe('failing')
    expect(problemLine({ symptom: 'cloud_address', since: now - 40 * minute }, now)).toBe(
      'Public address failing for 40m',
    )
    // nor one held until its own setup is done (ADR-0061)
    expect(problemHealth({ symptom: 'cloud_setup', since: 0 })).toBe('failing')
    expect(problemLine({ symptom: 'cloud_setup', since: now - 40 * minute }, now)).toBe('Needs setup for 40m')
    expect(problemHealth({ symptom: 'turn_errors', since: 0 })).toBe('warning')
    // a cloud session refused its connections still runs (ADR-0059)
    expect(problemHealth({ symptom: 'cloud_connections', since: 0 })).toBe('warning')
    expect(problemLine({ symptom: 'cloud_connections', since: now - 40 * minute }, now)).toBe(
      'Running without its secrets for 40m',
    )
    expect(problemMeaning({ symptom: 'cloud_connections', since: 0 })).toMatch(/Pause and resume the loop/)
    expect(problemMeaning({ symptom: 'turn_errors', since: 0 })).toBeUndefined()
  })
})

describe('the error-turn field', () => {
  it('shows off as empty', () => {
    expect(errorTurnsField(0)).toBe('')
    expect(errorTurnsField(3)).toBe('3')
  })

  it('takes 0 to 100, empty for off, and nothing else', () => {
    expect(errorTurnsValue('')).toBe(0)
    expect(errorTurnsValue(' 5 ')).toBe(5)
    expect(errorTurnsValue('100')).toBe(100)
    expect(errorTurnsValue('101')).toBeUndefined()
    expect(errorTurnsValue('2.5')).toBeUndefined()
    expect(errorTurnsValue('-1')).toBeUndefined()
  })
})

// #1145: a turn running past health_turn_minutes (#1142).
describe('turn_stuck', () => {
  const at = new Date(2026, 10, 1, 14, 2).getTime()
  const time = (ts: number) => new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

  it('says how long the turn has run and when it started', () => {
    expect(problemLine({ symptom: 'turn_stuck', since: at }, at + 130 * minute)).toBe(
      `Turn past its time limit: running for 2h, since ${time(at)}`,
    )
  })

  it('dates a start that was not today', () => {
    const line = problemLine({ symptom: 'turn_stuck', since: at }, at + 20 * 60 * minute)
    expect(line).toContain(
      `since ${new Date(at).toLocaleDateString([], { month: 'short', day: 'numeric' })}, `,
    )
  })

  it('is a warning, points to Kill, and says the hub never ends the turn', () => {
    const problem = { symptom: 'turn_stuck', since: at }
    expect(problemHealth(problem)).toBe('warning')
    expect(problemNext(problem)).toBe('Kill process ends it')
    expect(problemMeaning(problem)).toMatch(/never ends a turn/)
    expect(problemNext({ symptom: 'turn_errors', since: at })).toBeUndefined()
  })

  it('lowers only the first letter in the Fleet note, so the time keeps its case', () => {
    expect(healthNote([{ symptom: 'turn_stuck', since: at }], at + 130 * minute)).toBe(
      `turn past its time limit: running for 2h, since ${time(at)}`,
    )
  })
})

describe('turnMinutesField and turnMinutesValue', () => {
  it('shows off as empty and a limit as its minutes', () => {
    expect(turnMinutesField(0)).toBe('')
    expect(turnMinutesField(120)).toBe('120')
  })

  it('takes empty for off and minutes up to a day', () => {
    expect(turnMinutesValue('')).toBe(0)
    expect(turnMinutesValue(' 90 ')).toBe(90)
    expect(turnMinutesValue('1440')).toBe(1440)
    expect(turnMinutesValue('1441')).toBeUndefined()
    expect(turnMinutesValue('1.5')).toBeUndefined()
    expect(turnMinutesValue('-5')).toBeUndefined()
  })
})
