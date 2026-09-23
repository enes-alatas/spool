import type { LoopView } from './api'

// The fleet channel's addressing, as the compose box needs to see it before
// the post is sent (#286). The server decides who a post reaches; this
// module predicts it, so the rules below mirror `internal/route/router.go`
// and a difference between the two is a bug here, not a second policy.

// The server's mention grammar (`mentionRe`): an `@` at the start or after a
// character that is neither a word character nor another `@`, then a name.
// `\w` is ASCII in both RE2 and JavaScript, so the two agree on what a
// boundary is — `me@work` names nobody.
const MENTION = /(?:^|[^\w@])@([A-Za-z0-9_-]+)/g

// `@all`, the channel's broadcast (`route.BroadcastToken`).
export const BROADCAST = 'all'

// The unique lowercase mention tokens in a text, in order of first use.
export function mentionTokens(text: string): string[] {
  const seen = new Set<string>()
  for (const m of text.matchAll(MENTION)) seen.add(m[1].toLowerCase())
  return [...seen]
}

// The loops a post would reach, by name, in the order the loop list gives.
//
// A name matches a loop's name or its bot's username, case-insensitively,
// and reaches it only while it is in the fleet channel and not archived
// (`inGroup`). `@all` reaches every loop in the channel that is active — a
// paused loop has to be named (`broadcastTargets`). Names that match nothing
// reach nobody and are not an error: a human may be named too.
export function channelRecipients(text: string, loops: LoopView[]): string[] {
  const tokens = new Set(mentionTokens(text))
  const all = tokens.has(BROADCAST)
  return loops
    .filter((l) => {
      if (!l.in_fleet_channel || l.status === 'archived') return false
      if (all && l.status === 'active') return true
      return (
        tokens.has(l.name.toLowerCase()) ||
        (!!l.tg_bot_username && tokens.has(l.tg_bot_username.toLowerCase()))
      )
    })
    .map((l) => l.name)
}

// The mention being typed at the caret, if there is one: where its `@` is
// and what follows it so far. The same boundary rule as the grammar, so the
// box does not offer to complete what the server would not read as a
// mention.
export function mentionAt(text: string, caret: number): { start: number; query: string } | null {
  const before = text.slice(0, caret)
  const m = /(?:^|[^\w@])@([A-Za-z0-9_-]*)$/.exec(before)
  if (!m) return null
  return { start: caret - m[1].length - 1, query: m[1].toLowerCase() }
}

// What the box offers for a partial name: the loops in the channel whose
// name starts with it, then `@all` when it fits. Archived loops are gone and
// loops outside the channel would reach nobody, so offering either would be
// offering a mistake.
export function mentionCompletions(query: string, loops: LoopView[]): string[] {
  const q = query.toLowerCase()
  const names = loops
    .filter((l) => l.in_fleet_channel && l.status !== 'archived' && l.name.toLowerCase().startsWith(q))
    .map((l) => l.name)
  return BROADCAST.startsWith(q) ? [...names, BROADCAST] : names
}

// The text with the mention at `start` replaced by the chosen name and a
// space, and where the caret goes after it.
export function completeMention(
  text: string,
  start: number,
  caret: number,
  name: string,
): { text: string; caret: number } {
  const inserted = `@${name} `
  return { text: text.slice(0, start) + inserted + text.slice(caret), caret: start + inserted.length }
}
