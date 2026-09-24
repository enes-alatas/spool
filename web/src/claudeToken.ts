import type { LoopView } from './api'

// Which loops the missing-Claude-token banner is about (#305). A contained
// loop reads the operator's setup-token to start a turn; a bare one uses the
// host's own login and never reads it (`needsClaudeToken` in
// internal/loop/actor.go). So the banner is true of the loops that are not
// bare, and only of them — a fleet of bare loops is turning fine without
// one. A kind this build does not know counts as needing it, as it does on
// the server, which asks "not bare" rather than "docker". Archived loops
// never turn, so they are not in it; paused ones are, since resuming one
// would meet the missing token.
export function loopsNeedingClaudeToken(
  loops: readonly Pick<LoopView, 'name' | 'runtime' | 'status'>[],
): string[] {
  return loops.filter((l) => l.runtime !== 'bare' && l.status !== 'archived').map((l) => l.name)
}

// The banner's sentence, naming the loops it is true of. Up to three by
// name, which is a fleet an operator reads at a glance; past that a count,
// since a line of twenty handles is not something anyone reads.
export function missingTokenNotice(names: readonly string[]): string {
  const who =
    names.length === 1
      ? `@${names[0]} cannot start a turn: its workstation`
      : names.length <= 3
        ? `${listed(names.map((n) => `@${n}`))} cannot start a turn: their workstations`
        : `${names.length} contained loops cannot start a turn: their workstations`
  return `No Claude token is set, so ${who} will report down until one is.`
}

function listed(items: string[]): string {
  return items.length === 2
    ? `${items[0]} and ${items[1]}`
    : `${items.slice(0, -1).join(', ')} and ${items.at(-1)}`
}
