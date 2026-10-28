import { formatUsd } from './format'

// A loop's own daily cap (#652): at or over it the loop sleeps until
// midnight on the hub's clock, and only that loop does. Raising the cap past
// today's spend, or removing it, wakes it at once; Resume now lifts the
// plan cap only. These are
// the words the loop page and the New loop form say it in.

// The chips the cap is picked from, no cap first; any other amount goes in
// the field beside them.
export const DAILY_CAP_PRESETS = [0, 5, 10, 25]

export function presetLabel(usd: number): string {
  return usd ? `$${usd}` : 'no cap'
}

export function isPreset(cap: number | undefined): boolean {
  return DAILY_CAP_PRESETS.includes(cap ?? 0)
}

// What the custom field shows: empty for no cap.
export function dailyCapField(cap: number | undefined): string {
  return cap ? String(cap) : ''
}

// The field's number to send: 0 for an empty field, which is no cap, and
// undefined for one that is not an amount in dollars and cents. The server
// judges the range and says what it refused.
export function dailyCapValue(field: string): number | undefined {
  const value = field.trim().replace(/^\$/, '')
  if (value === '') return 0
  return /^\d+(\.\d{1,2})?$/.test(value) ? Number(value) : undefined
}

// The hint under the New loop form's chips. A cloud loop's spend is the
// hub's estimate from its token usage, the same the ~ on its cost marks, so
// its cap counts that.
export function dailyCapHint(cloud: boolean): string {
  const hint =
    "At the cap the loop sleeps until midnight on the hub's clock; raising the cap past today's spend, or removing it, wakes it."
  return cloud
    ? `${hint} A cloud loop's spend is estimated from its token usage, so the cap counts the estimate.`
    : hint
}

// Why a capped loop sleeps, under its next-wake countdown: its own cap, its
// Claude login's plan cap (#1075), or both, and what lifts each.
export function cappedNote(
  loop: { daily_cap_usd?: number; daily_cap_reached?: boolean },
  planCapped: boolean,
): string {
  if (!loop.daily_cap_reached) {
    return 'Capped: the plan cap holds it until the window resets. Messages wait in its inbox.'
  }
  const cap = loop.daily_cap_usd ? `its ${formatUsd(loop.daily_cap_usd)} daily cap` : 'its daily cap'
  if (planCapped) {
    return `Capped: it reached ${cap}, and its login's plan cap holds it. It wakes when both lift; messages wait in its inbox.`
  }
  return `Capped: it reached ${cap}. It sleeps until midnight on the hub's clock, or wakes now if you raise the cap past today's spend or remove it. Messages wait in its inbox.`
}

// The same while the loop is still in its turn: a cap takes it at the
// turn's end. Undefined when no cap will.
export function thenCappedNote(
  dailyCapReached: boolean | undefined,
  planCapped: boolean,
): string | undefined {
  if (dailyCapReached) return 'Then capped: it reached its daily cap, so it sleeps when this turn ends.'
  if (planCapped) return 'Then capped: it sleeps when this turn ends.'
  return undefined
}

// Which cap a capped Fleet row is under, for the state's title.
export function cappedBy(loop: { daily_cap_reached?: boolean }, planCapped: boolean): string {
  if (!loop.daily_cap_reached) return 'Capped by the plan cap'
  return planCapped ? 'Capped by its daily cap and the plan cap' : 'Capped by its daily cap'
}
