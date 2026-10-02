// The pace a loop keeps, as one range control shows it (#523): min wake,
// the tick interval and max wake on one track, in seconds. A [next-wake]
// trailer is clamped to min..max (internal/sched), and the tick is used only
// when a turn ends without one, so the three are kept in that order:
// min ≤ tick ≤ max. A tick above max is the pace that read as daily and ran
// every four hours; a tick below min is no setting anyone means.

export interface Pace {
  min: number
  tick: number
  max: number
}

// The stops the track snaps to, from a minute to a day. Spaced by index
// rather than by time, so five minutes and a day both have room.
export const PACE_STOPS = [
  60, 120, 300, 600, 900, 1200, 1800, 2700, 3600, 5400, 7200, 10800, 14400, 21600, 28800, 43200, 86400,
]

// The stops for a loop's current pace: a value set through the API that is
// not a stop is added where it falls, so opening the control never moves it.
export function paceStops(pace: Pace): number[] {
  return [...new Set([...PACE_STOPS, pace.min, pace.tick, pace.max])].sort((a, b) => a - b)
}

// Moves one handle to a stop, held between its neighbours.
export function movePace(pace: Pace, handle: keyof Pace, seconds: number): Pace {
  switch (handle) {
    case 'min':
      return { ...pace, min: Math.min(seconds, pace.tick) }
    case 'tick':
      return { ...pace, tick: Math.min(Math.max(seconds, pace.min), pace.max) }
    case 'max':
      return { ...pace, max: Math.max(seconds, pace.tick) }
  }
}

// A duration as the track labels it: 5m, 1h30m, 24h.
export function paceLabel(seconds: number): string {
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest ? `${hours}h${rest}m` : `${hours}h`
}

// What the control means, said once under it on both pages.
export const PACE_HINT =
  'The loop picks its next wake between min and max wake; the tick is used only when a turn ends without one. A message from anyone wakes it regardless.'

// The tick typed as whole minutes, for a pace no stop offers (#523): held
// between min and max wake like the handle, or null for anything but a whole
// number of minutes from 1, which leaves the pace as it was.
export function typeTick(pace: Pace, minutes: string): Pace | null {
  if (!/^\d+$/.test(minutes.trim()) || Number(minutes) < 1) return null
  return movePace(pace, 'tick', Number(minutes) * 60)
}
