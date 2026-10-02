import type { Reaction } from './api'

// A message's reactions as the room draws them: one chip per emoji, in the
// order each emoji first appeared, with who put it there (#535).
export interface ReactionChip {
  emoji: string
  reactors: string[]
}

export function reactionChips(reactions: readonly Reaction[]): ReactionChip[] {
  const byEmoji = new Map<string, string[]>()
  for (const reaction of reactions) {
    const reactors = byEmoji.get(reaction.emoji) ?? []
    reactors.push(reaction.reactor)
    byEmoji.set(reaction.emoji, reactors)
  }
  return [...byEmoji].map(([emoji, reactors]) => ({ emoji, reactors }))
}
