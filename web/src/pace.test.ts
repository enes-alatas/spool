import { describe, it, expect } from 'vitest'
import { movePace, paceLabel, paceStops, PACE_STOPS, typeTick } from './pace'

const pace = { min: 300, tick: 1800, max: 14400 }

describe('movePace', () => {
  // #523: a 24h tick under a 4h max read as a daily pace, and the loop kept
  // pacing itself to four hours. The tick handle stops at max wake instead.
  it('holds the tick at max wake', () => {
    expect(movePace(pace, 'tick', 86400)).toEqual({ ...pace, tick: 14400 })
  })

  it('holds the tick at min wake', () => {
    expect(movePace(pace, 'tick', 60)).toEqual({ ...pace, tick: 300 })
  })

  it('holds min and max wake on their side of the tick', () => {
    expect(movePace(pace, 'min', 3600)).toEqual({ ...pace, min: 1800 })
    expect(movePace(pace, 'max', 600)).toEqual({ ...pace, max: 1800 })
  })

  it('moves a handle freely within its neighbours', () => {
    expect(movePace(pace, 'max', 86400)).toEqual({ ...pace, max: 86400 })
    expect(movePace(pace, 'min', 60)).toEqual({ ...pace, min: 60 })
    expect(movePace(pace, 'tick', 3600)).toEqual({ ...pace, tick: 3600 })
  })
})

describe('paceStops', () => {
  it('is the fixed stops for a pace on them', () => {
    expect(paceStops(pace)).toEqual(PACE_STOPS)
  })

  // A value set through the API keeps its place rather than snapping.
  it('adds a value that is not a stop, in order', () => {
    const stops = paceStops({ ...pace, tick: 1500 })
    expect(stops).toContain(1500)
    expect(stops).toEqual([...stops].sort((a, b) => a - b))
  })
})

describe('paceLabel', () => {
  it('reads minutes under an hour, hours above', () => {
    expect(paceLabel(300)).toBe('5m')
    expect(paceLabel(5400)).toBe('1h30m')
    expect(paceLabel(86400)).toBe('24h')
  })
})

describe('typeTick', () => {
  it('sets a tick no stop offers', () => {
    expect(typeTick(pace, '25')).toEqual({ ...pace, tick: 1500 })
    expect(typeTick(pace, ' 90 ')).toEqual({ ...pace, tick: 5400 })
  })

  // Typed like dragged: a 24h tick under a 4h max wake stops at max wake.
  it('holds a typed tick between min and max wake', () => {
    expect(typeTick(pace, '1440')).toEqual({ ...pace, tick: 14400 })
    expect(typeTick(pace, '1')).toEqual({ ...pace, tick: 300 })
  })

  it('refuses anything but whole minutes from 1', () => {
    for (const bad of ['', ' ', '0', '-5', '1.5', '2h', 'abc']) {
      expect(typeTick(pace, bad)).toBeNull()
    }
  })
})
