import type { PromptsChatTeamLoop } from './api'

// One line for a member's handoffs, "to @b: review · from @a", or a plain
// "No handoffs" so an empty line never reads as a missing one.
export function handoffLine(loop: PromptsChatTeamLoop): string {
  if (loop.handoffs.length === 0) return 'No handoffs'
  return loop.handoffs
    .map(({ direction, loop: other, label }) => `${direction} @${other}${label ? `: ${label}` : ''}`)
    .join(' · ')
}

// Whether raw names a team's page on prompts.chat, or its API address, which the import reads
// as a team rather than a bot. The hub refuses whatever isn't one, so this
// only picks the route and checks nothing.
export function isTeamAddress(raw: string): boolean {
  try {
    return /^\/(?:api\/)?bots\/teams\/[^/]/.test(new URL(raw).pathname)
  } catch {
    return false
  }
}

// Whether raw names a single bot's page on prompts.chat, as the hub reads
// one: New team takes only a team, and says where a bot is imported
// instead. Like isTeamAddress it checks no host; the hub does.
export function isBotAddress(raw: string): boolean {
  try {
    const bot = /^\/bots\/([A-Za-z0-9][A-Za-z0-9_-]*)(?:\.md|\.json)?\/?$/.exec(new URL(raw).pathname)
    return !!bot && bot[1] !== 'teams' && bot[1] !== 'new'
  } catch {
    return false
  }
}
