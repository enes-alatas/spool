// Formatting shared by the pages. Small enough to inline, common enough that
// two copies would drift.

export function formatTokens(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n)
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

// The server's defaults (ADR-0022), for the moment before settings load.
const DEFAULT_ARM_PERCENT = 40
const DEFAULT_FORCE_PERCENT = 70

export interface RotationThresholds {
  context_arm_percent: number
  context_force_percent: number
}
