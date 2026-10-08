// Formatting shared by the pages. Small enough to inline, common enough that
// two copies would drift.

export function formatTokens(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n)
}

// A cost the operator reads as money. Cents always, so a column of them
// aligns and $0.00 reads as a measured zero rather than a missing value.
export function formatUsd(n: number): string {
  return `$${n.toFixed(2)}`
}

// What the fleet has spent today: the sum of the real per-loop values, not of
// the rounded ones on screen. Ten loops at a third of a cent each show $0.00
// on every row and $0.03 in the header — the header is right, and a total
// assembled from the displayed strings would not be.
export function sumCostToday(loops: { cost_today_usd: number }[]): number {
  return loops.reduce((total, loop) => total + loop.cost_today_usd, 0)
}

// A loop's part of what the fleet spent today, as a whole percent: the row's
// answer to "which loop is eating the plan". Taken from the real values, like
// the total, so four rows that each read $0.00 still divide a day that cost
// something. Zero on a day the fleet has spent nothing: every loop's part of
// nothing is none of it, and an empty column read as a missing one.
export function spendShare(cost: number, total: number): number {
  if (!(total > 0)) return 0
  return Math.round((cost / total) * 100)
}

// The colour a bar of the room's money is drawn in, graded along its percent
// rather than stepped: the muted ink of the figures around it at nothing, the
// active orange at half, the danger red at all of it. Two loops at 40% and 45%
// of the fleet's day are told apart by length, and a loop at 90% does not wear
// the same colour as one at 55%. Mixed from the tokens, so a palette change
// carries through.
export function gradedColor(pct: number): string {
  const p = Math.min(Math.max(pct, 0), 100)
  if (p <= 50) return `color-mix(in oklab, var(--active) ${p * 2}%, var(--text-muted))`
  return `color-mix(in oklab, var(--danger) ${(p - 50) * 2}%, var(--active))`
}

// How full a context window is, and how loudly to say so — in the operator's
// own terms. Warm is "rotation is armed, it will happen at the next quiet
// boundary", hot is "past the ceiling, the next turn rotates first". Fixed
// tones could not say either: they were 75/90 against configurable
// thresholds that default to 40/70, so the gauge stayed calm through the
// state that actually triggers rotation.
export function fillTone(pct: number, thresholds?: RotationThresholds): string {
  const arm = thresholds?.context_arm_percent ?? DEFAULT_ARM_PERCENT
  const force = thresholds?.context_force_percent ?? DEFAULT_FORCE_PERCENT
  if (pct >= force) return 'hot'
  if (pct >= arm) return 'warm'
  return ''
}

// Whether the server actually reported an occupancy. `api.ts` types the field
// as `number` because that is the API as it will be, but a server older than
// it sends nothing and it arrives as undefined — which prints as a percent
// sign with no quantity in front of it. The parameter admits that, so the
// guard is a real question rather than one tsc can prove.
//
// Falsiness cannot ask it: the server computes the fill in integer arithmetic
// (`FillPercent`), so a measured loop holding ~1,500 tokens of a 200k window
// truncates to a true, reported 0%.
export function hasFillPct(pct: number | undefined): boolean {
  return typeof pct === 'number'
}

// The server's defaults (ADR-0022), for the moment before settings load.
const DEFAULT_ARM_PERCENT = 40
const DEFAULT_FORCE_PERCENT = 70

export interface RotationThresholds {
  context_arm_percent: number
  context_force_percent: number
}

// When the loop next wakes, as the countdown shows it: 0, the empty dot, while
// its model is refused. The schedule still holds a tick then, but the actor
// skips it until the model is changed (#289), so a countdown would promise a
// wake that does not come.
export function nextWake(loop: { next_tick_at: number; model_refusal: string }): number {
  return loop.model_refusal ? 0 : loop.next_tick_at
}

// Whether the loop is in a turn, waking into one or running it (#524). Its
// next_tick_at is no decided wake then: a tick that fires resets it to the
// fallback interval, and only the turn's end sets the real one, so a
// countdown would show a wake the loop has not chosen.
export function inTurn(state: string): boolean {
  return state === 'waking' || state === 'busy'
}
