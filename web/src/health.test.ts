import { describe, expect, it } from 'vitest'
import { errorTurnsField, errorTurnsValue, healthNote, openFor, problemHealth, problemLine } from './health'

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
    expect(problemHealth({ symptom: 'turn_errors', since: 0 })).toBe('warning')
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
