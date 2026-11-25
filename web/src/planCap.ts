import type { LoginPlanUsage, PlanCap, PlanUsage, PlanUsages, PlanWindowKey } from './api'
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

// That a Resume now is holding, and until when: the Settings status line
// and, beside Cap again, a resumed ring's details (#1688). Undefined while
// no Resume now holds.
export function resumedLine(usage: PlanUsage | undefined, now: number): string | undefined {
  const hold = resumedHold(usage, now)
  return hold === undefined ? undefined : `Resumed: ${hold}`
}

// The hold alone, "the cap holds off until the window resets in 41m.", which
// the Fleet page's resumed banner says after its own lead (#1688).
export function resumedHold(usage: PlanUsage | undefined, now: number): string | undefined {
  if (!usage?.resumed_until || usage.resumed_until <= now) return undefined
  return `the cap holds off until the window ${formatResets(usage.resumed_until, now)}.`
}

// Whether Cap again is offered for a login (#1688): no cap holds, the hub
// says a Resume now holds one off, and that hold is still ahead on this
// clock. The hub's resumed_cap is as of the last read, so between the hold
// running out here and the next read the words would be gone while the
// button stayed; one test keeps them together everywhere it shows.
export function capAgainOffered(usage: PlanUsage | undefined, now: number): boolean {
  return !!usage && !usage.cap && !!usage.resumed_cap && resumedHold(usage, now) !== undefined
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
  const resumed = resumedLine(usage, now)
  if (resumed) return resumed
  const reading = (['five_hour', 'seven_day'] as const)
    .filter((key) => usage[key])
    .map((key) => `${WINDOW_NAME[key]} at ${Math.round(usage[key]!.used_percent)}%`)
  return `Not capped: ${reading.join(', ')}.`
}

export function capitalize(s: string): string {
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
// usage when the hub holds more than one, and none on a hub with one,
// which reads as the strip always has.
export function usageLogins(usages: PlanUsages | undefined): LoginPlanUsage[] | undefined {
  const logins = usages?.logins
  return logins && logins.length > 1 ? logins : undefined
}

// The default login's plan usage, which the hub always lists, first: the
// strip's bars and the fleet's banner on a hub with one login.
export function defaultUsage(usages: PlanUsages | undefined): LoginPlanUsage | undefined {
  return loginUsage(usages, 'default')
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

// A login's plan usage, cap and all: its entry in logins.
export function loginUsage(usages: PlanUsages | undefined, login: string): LoginPlanUsage | undefined {
  return usages?.logins.find((entry) => entry.name === login)
}

// Whether a loop's login is capped, so the loop sleeps at its turn's end.
export function loopPlanCapped(usages: PlanUsages | undefined, loop: { claude_login: string }): boolean {
  return !!loginUsage(usages, loopLogin(loop))?.cap
}

// The logins whose cap holds, each with the read the banner says it from:
// every capped login on a hub with more than one, and the default on a hub
// with one, whose banner speaks of the fleet.
export function cappedLogins(usages: PlanUsages | undefined): LoginPlanUsage[] {
  return (usageLogins(usages) ?? [defaultUsage(usages)]).filter(
    (login): login is LoginPlanUsage => !!login?.cap,
  )
}

// The logins whose loops all run in the cloud (#1303): at least one loop and
// none of it bare or docker. A cloud turn reports no rate-limit event
// (ADR-0059), so such a login's usage is the hub's own read of it after a
// cloud loop's turn (#1285), not a loop's stream. Only the default can be
// one: a cloud loop runs on it.
export function cloudOnlyLogins(loops: { runtime: string; claude_login: string }[] | undefined): Set<string> {
  const contained = new Set<string>()
  const cloud = new Set<string>()
  for (const loop of loops ?? []) (loop.runtime === 'cloud' ? cloud : contained).add(loopLogin(loop))
  return new Set([...cloud].filter((login) => !contained.has(login)))
}

// Whether a login's reading still counts: a cap holding, or a window not yet
// past its reset. The cap acts on such a reading whatever runtime its loops
// are, so a cloud-only login with one reads like any other.
export function liveReading(usage: PlanUsage | undefined): boolean {
  if (!usage || usage.unknown !== undefined) return false
  if (usage.cap) return true
  return [usage.five_hour, usage.seven_day].some((window) => !!window && !window.reset)
}

// How a login's figures read for its runtime (#1303, #1738): 'pending' for
// a cloud-only login with nothing live, whose usage comes with the hub's
// read after a cloud loop's next turn; undefined for any other, a
// cloud-only login with a live reading included.
export type CloudReading = 'pending' | undefined

export function cloudReading(usage: PlanUsage | undefined, cloudOnly: boolean): CloudReading {
  return cloudOnly && !liveReading(usage) ? 'pending' : undefined
}

// A cloud-only login with nothing live, in place of its figures, and why.
// A sign-in without the user:profile scope is never read (#1285): a setup
// token, or one kept before v0.6.0 kept a sign-in's scopes.
export const CLOUD_ONLY_USAGE = 'usage unknown until a cloud turn'
export const CLOUD_ONLY_WHY =
  "Its loops all run in the cloud, so the hub reads this login's plan usage after a cloud loop's turn, at most every five minutes, and the cap acts on it from then. A setup token, or a sign-in from before v0.6.0, is never read: choose Sign in again under Settings → Claude logins."

// Where a reading came from, beside its age: the hub's own read, which
// follows a cloud loop's turn, so a quiet login's figures are as old as its
// last one. Undefined for a reading from a loop's stream.
export function readingFrom(usage: PlanUsage | undefined): string | undefined {
  return usage?.source === 'usage_command' ? "read by the hub after a cloud loop's turn" : undefined
}

// The line beside the cap in Settings: which logins it acts on today, with
// a cloud-only one that has nothing live named apart, as not yet among them
// (#1303, #1738). Undefined until the logins are read.
export function capReach(logins: LoginPlanUsage[] | undefined, cloudOnly: Set<string>): string | undefined {
  if (!logins || logins.length === 0) return undefined
  const names = logins.map((login) => login.name)
  const pending = logins
    .filter((login) => cloudReading(login, cloudOnly.has(login.name)) === 'pending')
    .map((l) => l.name)
  const acting = names.filter((name) => !pending.includes(name))
  if (pending.length === 0) {
    return `The cap acts on ${names.length === 1 ? 'the login' : 'every login'} here (${listed(names)}).`
  }
  const why = "the hub reads its usage after a cloud loop's turn"
  return acting.length === 0
    ? `The cap acts on no login here yet: ${listed(pending)} ${runsOnly(pending)}, and ${why}.`
    : `The cap acts on ${listed(acting)}. Not yet on ${listed(pending)}, which ${runsOnly(pending)}: ${why}.`
}

function runsOnly(names: string[]): string {
  return names.length === 1 ? 'runs only cloud loops' : 'run only cloud loops'
}

function listed(names: string[]): string {
  if (names.length === 1) return names[0]
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}
