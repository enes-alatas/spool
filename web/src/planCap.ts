import type { LoginPlanUsage, PlanCap, PlanUsage, PlanWindowKey } from './api'
import { formatResets } from './format'

// The plan cap (#650, #651), which works per Claude login (#1075): above a
// window's threshold the loops on that login sleep at their next quiet
// boundary and wake when the window resets. The thresholds are the same for
// every login. These are the words the Fleet banner and the Settings status
// say it in.

const WINDOW_NAME: Record<PlanWindowKey, string> = { five_hour: '5-hour', seven_day: '7-day' }

// Why a login is capped, naming each window over its threshold with its
// reading, then which loops sleep and when they wake: the loops named, or
// every loop on a hub with one login, where sleeps is undefined. Undefined
// while nothing is capped.
export function capReason(usage: PlanUsage | undefined, now: number, sleeps?: string[]): string | undefined {
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
  return `${capitalize(over.join(' and '))}. ${capWake(cap, now, sleeps)}`
}

// Which loops a cap sleeps and when they wake, the end of capReason and,
// alone, a capped ring's details (#1076).
export function capWake(cap: PlanCap, now: number, sleeps?: string[]): string {
  const wake = formatResets(cap.until, now)
  // With two windows over, the loops wait for the later of the two: waking at
  // the earlier would cap again on the first turn.
  const until = cap.windows.length > 1 ? `the later one ${wake}` : `it ${wake}`
  return `${sleepers(sleeps)} until ${until}; messages wait in their inboxes.`
}

function sleepers(loops: string[] | undefined): string {
  if (loops === undefined) return 'Loops sleep'
  if (loops.length === 0) return 'No loop runs on it; one moved to it sleeps'
  if (loops.length === 1) return `${loops[0]} sleeps`
  return `${loops.slice(0, -1).join(', ')} and ${loops[loops.length - 1]} sleep`
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

// The logins the plan strip gives a ring each (ADR-0060): every login's
// usage when the hub holds more than one, and none on a hub with one, or
// one older than #1073, which reads as the strip always has.
export function usageLogins(usage: PlanUsage | undefined): LoginPlanUsage[] | undefined {
  const logins = usage?.logins
  return logins && logins.length > 1 ? logins : undefined
}

// A login's name as its ring fits it: a name up to 8 characters whole, a
// longer one cut short, since the ring's details name it in full.
export function ringName(name: string): string {
  return name.length > 8 ? `${name.slice(0, 7)}…` : name
}

// What a ring's two windows are, on hover: the strip has no legend.
export const RING_KEY = '5-hour round the outside, 7-day inside. Click for the figures.'

// The login a loop's plan cap is: its own, the default for a bare or cloud
// loop ('').
export function loopLogin(loop: { claude_login: string }): string {
  return loop.claude_login || 'default'
}

// A login's plan usage, cap and all: the default's is the top-level read,
// on a hub older than #1073 too, and any other its entry in logins.
export function loginUsage(usage: PlanUsage | undefined, login: string): PlanUsage | undefined {
  if (login === 'default') return usage
  return usage?.logins?.find((entry) => entry.name === login)
}

// Whether a loop's login is capped, so the loop sleeps at its turn's end.
export function loopPlanCapped(usage: PlanUsage | undefined, loop: { claude_login: string }): boolean {
  return !!loginUsage(usage, loopLogin(loop))?.cap
}

// The logins whose cap holds, each with the read the banner says it from.
// Every capped login on a hub with more than one; the top-level read on a
// hub with one, or one older than #1073, whose banner speaks of the fleet.
export function cappedLogins(usage: PlanUsage | undefined): LoginPlanUsage[] {
  const logins = usageLogins(usage)
  if (logins) return logins.filter((login) => login.cap)
  return usage?.cap ? [{ ...usage, name: 'default' }] : []
}
