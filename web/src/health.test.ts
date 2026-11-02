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
  minutesField,
  minutesNote,
  minutesValue,
  stepField,
  stopIndex,
  stopLabel,
  TURN_MINUTE_STOPS,
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

describe('minutesField and minutesValue', () => {
  it('shows off as empty and a limit as its minutes', () => {
    expect(minutesField(0)).toBe('')
    expect(minutesField(120)).toBe('120')
  })

  it('takes empty for off and minutes up to a day', () => {
    expect(minutesValue('')).toBe(0)
    expect(minutesValue(' 90 ')).toBe(90)
    expect(minutesValue('1440')).toBe(1440)
    expect(minutesValue('1441')).toBeUndefined()
    expect(minutesValue('1.5')).toBeUndefined()
    expect(minutesValue('-5')).toBeUndefined()
  })
})

// #1146: a wake owed past health_wake_grace_minutes with no turn (#1143).
describe('wake_missed', () => {
  const at = new Date(2026, 10, 1, 14, 2).getTime()
  const time = (ts: number) => new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

  it('says the loop was due to wake and has not, and since when', () => {
    expect(problemLine({ symptom: 'wake_missed', since: at }, at + 25 * minute)).toBe(
      `Due to wake and hasn't: waiting 25m, since ${time(at)}`,
    )
  })

  it('is failing, since the loop is doing nothing', () => {
    expect(problemHealth({ symptom: 'wake_missed', since: at })).toBe('failing')
    expect(problemMeaning({ symptom: 'wake_missed', since: at })).toMatch(/stopped answering/)
    expect(problemNext({ symptom: 'wake_missed', since: at })).toBeUndefined()
  })
})

// #1146: Settings' sliders snap to steps; the number above takes any value.
describe('the health limit controls', () => {
  it('puts a value on the last step at or below it', () => {
    expect(stopIndex(TURN_MINUTE_STOPS, 0)).toBe(0)
    expect(stopIndex(TURN_MINUTE_STOPS, 120)).toBe(TURN_MINUTE_STOPS.indexOf(120))
    expect(stopIndex(TURN_MINUTE_STOPS, 100)).toBe(TURN_MINUTE_STOPS.indexOf(90))
    expect(stopIndex(TURN_MINUTE_STOPS, 1440)).toBe(TURN_MINUTE_STOPS.length - 1)
  })

  it('every step list starts at off and climbs', () => {
    expect(TURN_MINUTE_STOPS[0]).toBe(0)
    expect([...TURN_MINUTE_STOPS].sort((a, b) => a - b)).toEqual(TURN_MINUTE_STOPS)
  })

  it('steps the number by one within its range, empty for off', () => {
    expect(stepField('120', 1, 1440)).toBe('121')
    expect(stepField('1', -1, 1440)).toBe('')
    expect(stepField('', 1, 100)).toBe('1')
    expect(stepField('', -1, 100)).toBe('')
    expect(stepField('1440', 1, 1440)).toBe('1440')
    expect(stepField('abc', 1, 100)).toBe('1')
  })

  it('says minutes at a glance from an hour up', () => {
    expect(minutesNote(45)).toBe('')
    expect(minutesNote(60)).toBe('1h')
    expect(minutesNote(90)).toBe('1h 30m')
    expect(minutesNote(1440)).toBe('1d')
  })

  it('labels a step', () => {
    expect(stopLabel(0, true)).toBe('off')
    expect(stopLabel(15, true)).toBe('15m')
    expect(stopLabel(240, true)).toBe('4h')
    expect(stopLabel(20, false)).toBe('20')
  })
})
