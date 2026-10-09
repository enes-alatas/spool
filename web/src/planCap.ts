import type { PlanUsage, PlanWindowKey } from './api'
import { formatResets } from './format'

// The plan cap (#650, #651): above a window's threshold every loop sleeps at
// its next quiet boundary and wakes when the window resets. These are the
// words the Fleet banner and the Settings status say it in.

const WINDOW_NAME: Record<PlanWindowKey, string> = { five_hour: '5-hour', seven_day: '7-day' }

// Why the fleet is capped, naming each window over its threshold with its
// reading, then when the loops wake. Undefined while nothing is capped.
export function capReason(usage: PlanUsage | undefined, now: number): string | undefined {
  const cap = usage?.cap
  if (!cap || cap.windows.length === 0) return undefined
  const over = cap.windows.map((key) => {
    const window = usage[key]
    const name = WINDOW_NAME[key]
    if (!window) return `the ${name} window is over its threshold`
    const at = `${Math.round(window.used_percent)}%`
    return window.cap_percent
      ? `the ${name} window is at ${at}, over its ${window.cap_percent}% threshold`
      : `the ${name} window is at ${at}`
  })
  const wake = formatResets(cap.until, now)
  // With two windows over, the loops wait for the later of the two: waking at
  // the earlier would cap again on the first turn.
  const until = cap.windows.length > 1 ? `the later one ${wake}` : `it ${wake}`
  return `${capitalize(over.join(' and '))}. Loops sleep until ${until}; messages wait in their inboxes.`
}

// The status line over the Settings thresholds when nothing is capped:
// where usage stands, a Resume now that is holding, or that the cap cannot
// fire because nothing has been read. Undefined while the read is loading,
// which says nothing about usage yet.
export function capIdleStatus(usage: PlanUsage | undefined, now: number): string | undefined {
  if (!usage) return undefined
  if (usage.unknown !== undefined || (!usage.five_hour && !usage.seven_day)) {
    return 'Usage is unknown, so the cap cannot fire.'
  }
  if (usage.resumed_until && usage.resumed_until > now) {
    return `Resumed: the cap holds off until the window ${formatResets(usage.resumed_until, now)}.`
  }
  const reading = (['five_hour', 'seven_day'] as const)
    .filter((key) => usage[key])
    .map((key) => `${WINDOW_NAME[key]} at ${Math.round(usage[key]!.used_percent)}%`)
  return `Not capped: ${reading.join(', ')}.`
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

// What the two threshold fields show and whether they can be saved. An empty
// field is the cap turned off, sent as 0, so it is sendable; anything that
// is not digits is not. The server judges the numbers themselves and says
// what it refused.
export interface CapDraft {
  fiveHour: string
  sevenDay: string
}

export function capFields(fiveHour: number, sevenDay: number): CapDraft {
  return { fiveHour: fiveHour ? String(fiveHour) : '', sevenDay: sevenDay ? String(sevenDay) : '' }
}

export function capGate(
  stored: CapDraft | null,
  draft: CapDraft | null,
): { shown: CapDraft | null; changed: boolean; sendable: boolean } {
  const shown = draft ?? stored
  const changed =
    !!draft &&
    !!stored &&
    (draft.fiveHour.trim() !== stored.fiveHour || draft.sevenDay.trim() !== stored.sevenDay)
  const sendable =
    changed && !!shown && capValue(shown.fiveHour) !== undefined && capValue(shown.sevenDay) !== undefined
  return { shown, changed, sendable }
}

// A field's number to send: 0 for an empty field, undefined for one that is
// not a whole number.
export function capValue(field: string): number | undefined {
  const value = field.trim()
  if (value === '') return 0
  return /^\d+$/.test(value) ? Number(value) : undefined
}
