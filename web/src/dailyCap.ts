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

// What a capped loop's note and title read from its view: its own daily
// cap, and the usage limit that refused one of its turns (#1657).
type CapHolds = {
  daily_cap_usd?: number
  daily_cap_reached?: boolean
  usage_limited?: boolean
  capped_until?: number
}

// A usage limit that named no reset holds the loop until the operator
// continues it, so its view carries no time to wake at.
export function waitsForContinue(loop: CapHolds): boolean {
  return !!loop.usage_limited && !loop.capped_until
}

// The words of a list: "a", "a and b", "a, b, and c". Clauses take the
// comma at two as well: "it reached its cap, and the plan cap holds it".
function listed(items: string[], clauses = false): string {
  if (items.length < 3 && !clauses) return items.join(' and ')
  return items.length < 2
    ? items.join('')
    : `${items.slice(0, -1).join(', ')}, and ${items[items.length - 1]}`
}

const LIMIT_REFUSED = "its Claude plan's usage limit refused a turn"
const PRESS = `When your plan's usage is back, press Acknowledge on the "Held by the usage limit" line to continue it.`

// Why a capped loop sleeps, under its next-wake countdown: its own cap, its
// Claude login's plan cap (#1075), the plan's usage limit (#1657), or more
// than one, and what lifts each. A capped loop held by none the view names
// is under the plan cap, as a hub too old to say otherwise holds it.
export function cappedNote(loop: CapHolds, planCapped: boolean): string {
  const cap = loop.daily_cap_usd ? `its ${formatUsd(loop.daily_cap_usd)} daily cap` : 'its daily cap'
  const holds = [
    loop.daily_cap_reached && `it reached ${cap}`,
    (planCapped || (!loop.daily_cap_reached && !loop.usage_limited)) && "its login's plan cap holds it",
    loop.usage_limited && LIMIT_REFUSED,
  ].filter((hold): hold is string => !!hold)
  const waits = waitsForContinue(loop)
  if (holds.length > 1) {
    const all = holds.length === 2 ? 'both' : 'all three'
    return waits
      ? `Capped: ${listed(holds, true)}. It wakes when ${all} lift; messages wait in its inbox. ${PRESS}`
      : `Capped: ${listed(holds, true)}. It wakes when ${all} lift; messages wait in its inbox.`
  }
  if (loop.usage_limited) {
    return waits
      ? `Capped: ${LIMIT_REFUSED} without saying when it resets, so it waits for you. ${PRESS} Messages wait in its inbox.`
      : `Capped: ${LIMIT_REFUSED}. It wakes when the limit resets; messages wait in its inbox.`
  }
  if (loop.daily_cap_reached) {
    return `Capped: it reached ${cap}. It sleeps until midnight on the hub's clock, or wakes now if you raise the cap past today's spend or remove it. Messages wait in its inbox.`
  }
  return 'Capped: the plan cap holds it until the window resets. Messages wait in its inbox.'
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

// Which caps a capped Fleet row is under, for the state's title.
export function cappedBy(loop: CapHolds, planCapped: boolean): string {
  const holds = [
    loop.daily_cap_reached && 'its daily cap',
    (planCapped || (!loop.daily_cap_reached && !loop.usage_limited)) && 'the plan cap',
    loop.usage_limited && 'the usage limit',
  ].filter((hold): hold is string => !!hold)
  return `Capped by ${listed(holds)}`
}
