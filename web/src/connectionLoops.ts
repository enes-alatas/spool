import type { Connection, LoopView } from './api'
import { givesGitHubIdentity } from './connections'

// Attaching a shared connection to loops from the Connections page (#1321),
// by the rules the loop page's attach list keeps (`LoopConnections`): a
// cloud loop can't hold a GitHub App, and a loop has one GitHub identity.
// The hub refuses both too; saying so beside the loop beats a refusal after
// the save.

// A loop as the picker offers it: refused says why it can't take the
// connection, and is undefined when it can.
export interface LoopOption {
  name: string
  refused?: string
}

export function loopOptions(
  c: Pick<Connection, 'name' | 'kind' | 'config'>,
  loops: readonly LoopView[],
  connections: readonly Connection[],
): LoopOption[] {
  const byName = new Map(connections.map((other) => [other.name, other]))
  return loops.map((loop) => {
    if (loop.runtime === 'cloud' && c.kind === 'github-app')
      return { name: loop.name, refused: "a cloud loop can't hold a GitHub App" }
    if (givesGitHubIdentity(c)) {
      const identity = (loop.connections ?? []).find(
        (a) => a.name !== c.name && givesGitHubIdentity(byName.get(a.name) ?? { kind: a.kind }),
      )
      if (identity) return { name: loop.name, refused: `has its GitHub identity from ${identity.name}` }
    }
    return { name: loop.name }
  })
}

// The calls that take a connection from the loops it is on to the loops
// picked: one attach per loop added, one detach per loop taken off.
export function attachPlan(
  current: readonly string[],
  picked: readonly string[],
): { attach: string[]; detach: string[] } {
  return {
    attach: picked.filter((loop) => !current.includes(loop)),
    detach: current.filter((loop) => !picked.includes(loop)),
  }
}
