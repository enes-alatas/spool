// A loop's doing line (#750): what it said it is working on, and how long
// ago. The loop sets it with a trailer and keeps it until it says otherwise,
// so an old one may no longer be true.

import { formatAgo } from './format'

const DAY_MS = 24 * 60 * 60 * 1000

// Said more than a day ago: shown dimmed, since a loop that has moved on
// without saying so leaves it standing.
export function doingStale(at: number, now: number): boolean {
  return now - at > DAY_MS
}

// How long ago the loop said it, "4m ago", or nothing when it never did.
export function doingAge(at: number, now: number): string {
  return at ? formatAgo(at, now) : ''
}
