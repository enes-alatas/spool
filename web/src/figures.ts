// The Activity page's figures strip (#1301): a loop's figures from Langfuse,
// as the hub serves them (#1300), said in the page's words.

import type { Figures, FiguresTurn, FiguresWindow, LoopFigures } from './api'
import { formatAgo } from './format'

export const FIGURES_WINDOWS: FiguresWindow[] = ['1h', '24h', '7d', '30d']
export const DEFAULT_WINDOW: FiguresWindow = '24h'

// Where an operator turns traces on: the control room holds no Langfuse
// settings (#1298), the hub's env file does (#1388).
export const LANGFUSE_DOCS =
  'https://github.com/enes-alatas/spool/blob/main/docs/OPERATIONS.md#langfuse-beside-the-hub'

// A window picked from the URL, or the default for anything else.
export function figuresWindow(raw: string | null): FiguresWindow {
  return FIGURES_WINDOWS.find((w) => w === raw) ?? DEFAULT_WINDOW
}

// A loop's figures for a window, cached apart from every other window's,
// so picking another window asks the hub for that one.
export function figuresKey(loop: string, window: FiguresWindow) {
  return ['figures', loop, window] as const
}

// How often to ask again: at the hub's own refresh period rather than the
// room's 15s default, so an open page sees pending resolve and figures turn
// stale without asking for every loop's far faster than the hub's cache
// can change; never under the default. Not at all while there is no
// answer, which is a hub without the route.
export function figuresRefetch(f: LoopFigures | undefined): number | false {
  return f ? Math.max(f.refresh_sec, 15) * 1000 : false
}

// A count and its noun, one turn or 2 turns.
export function count(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}

// The turn the line under the bars reads: the one under the pointer, or
// the newest. A refetch can shorten the turns under a held pointer.
export function turnAt(length: number, at: number | null): number {
  return at === null || at >= length ? length - 1 : at
}

// A duration as it reads at a glance: 900ms, 41s, 3m 2s, 1h 4m.
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return s % 60 ? `${m}m ${s % 60}s` : `${m}m`
  return m % 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${Math.floor(m / 60)}h`
}

// The window's tokens and spend over its turns; zero turns read as zero.
export function perTurn(f: Figures): {
  cost: number
  input: number
  output: number
  cacheRead: number
} {
  const n = f.turns || 1
  return {
    cost: f.cost_usd / n,
    input: Math.round(f.tokens.input / n),
    output: Math.round(f.tokens.output / n),
    cacheRead: Math.round(f.tokens.cache_read / n),
  }
}

// The tools by calls, the first few by name and the rest folded into one
// row, so a long tail doesn't push the strip down the page.
export const TOOLS_SHOWN = 6
export interface ToolRow {
  name: string
  calls: number
  failures: number
  // the calls against the most-called tool's, for the bar
  share: number
  other?: number
}
export function toolMix(tools: Figures['tools']): ToolRow[] {
  const sorted = [...tools].sort((a, b) => b.calls - a.calls)
  const top = sorted[0]?.calls || 1
  const rows: ToolRow[] = sorted
    .slice(0, TOOLS_SHOWN)
    .map((t) => ({ name: t.name, calls: t.calls, failures: t.failures, share: t.calls / top }))
  const rest = sorted.slice(TOOLS_SHOWN)
  if (rest.length > 0) {
    const calls = rest.reduce((n, t) => n + t.calls, 0)
    rows.push({
      name: rest.length === 1 ? rest[0].name : `${rest.length} other tools`,
      calls,
      failures: rest.reduce((n, t) => n + t.failures, 0),
      share: calls / top,
      other: rest.length,
    })
  }
  return rows
}

// Each turn's spend against the window's most expensive, for the bars.
export function turnShares(turns: FiguresTurn[]): number[] {
  const top = Math.max(0, ...turns.map((t) => t.cost_usd))
  return turns.map((t) => (top > 0 ? t.cost_usd / top : 0))
}

// What the strip says in place of figures, or null when it has them.
export function figuresLine(f: LoopFigures): string | null {
  switch (f.langfuse) {
    case 'ok':
    case 'stale':
      return f.figures ? null : 'No figures yet.'
    case 'pending':
      return 'Traces are on; the first figures arrive at the next fetch.'
    case 'off':
      return 'Traces are off, so there are no figures to show.'
    case 'unsupported':
      return 'A cloud loop sends no traces, so it has no figures.'
  }
}

// How old the figures are: always said, and as a warning once the hub
// couldn't refresh them, so last figures never pass for current ones.
export function figuresAge(f: LoopFigures, now: number): { text: string; stale: boolean } | null {
  if (!f.figures || f.fetched_at === 0) return null
  const ago = formatAgo(f.fetched_at, now)
  return f.langfuse === 'stale'
    ? { text: `Langfuse isn't answering: these are from ${ago}`, stale: true }
    : { text: `from Langfuse · ${ago}`, stale: false }
}

// The overview's cards (#1301): every loop's figures for the window, a
// card each. A hub with no Langfuse says so once, not on every card; a
// cloud loop, which never has figures, doesn't count against that.
export function allOff(answers: LoopFigures[]): boolean {
  return (
    answers.some((a) => a.langfuse === 'off') &&
    answers.every((a) => a.langfuse === 'off' || a.langfuse === 'unsupported')
  )
}

// What a card says in place of figures, short, or null when it has them.
export function cardLine(f: LoopFigures): string | null {
  switch (f.langfuse) {
    case 'ok':
    case 'stale':
      return f.figures ? null : 'no figures yet'
    case 'pending':
      return 'traces on, figures at the next fetch'
    case 'off':
      return 'traces off'
    case 'unsupported':
      return 'a cloud loop sends no traces'
  }
}
