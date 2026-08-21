// Formatting shared by the pages. Small enough to inline, common enough that
// two copies would drift.

export function formatTokens(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n)
}

// How full a context window is, and how loudly to say so. Nothing is wrong at
// 60%; past 90% the loop is close enough to overflow that it should catch the
// eye on a card the operator is only glancing at.
export function fillTone(ratio: number): string {
  if (ratio >= 0.9) return 'hot'
  if (ratio >= 0.75) return 'warm'
  return ''
}
