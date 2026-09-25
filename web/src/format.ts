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
