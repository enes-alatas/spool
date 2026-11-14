// A channel's chat on a surface, as its page words it (ADR-0070, #1509): the
// Telegram group or Slack channel bound once for all the channel's members.
// The logic the component needn't hold: what a surface's chat is called,
// which members keep an add from being offered, what each member's status
// says, and what a refused bind tells the operator.
import { ApiError, type ChannelChat, type LoopView } from './api'
import { loopSurface } from './slack'

export type ChatSurface = 'telegram' | 'slack'

// The surfaces a chat can be added on, in the order the page offers them.
export const CHAT_SURFACES: readonly ChatSurface[] = ['telegram', 'slack']

// What a surface's chat and a loop's presence there are called.
export interface SurfaceWords {
  // The surface by name: "Telegram".
  name: string
  // One chat there: "group".
  chat: string
  // What puts a loop on it: "bot".
  bot: string
}

export function surfaceWords(surface: string): SurfaceWords {
  return surface === 'slack'
    ? { name: 'Slack', chat: 'channel', bot: 'app' }
    : { name: 'Telegram', chat: 'group', bot: 'bot' }
}

// The chat's surface, as this build words it; a surface it doesn't know
// reads as Telegram's, whose chats came first.
export function chatSurface(surface: string): ChatSurface {
  return surface === 'slack' ? 'slack' : 'telegram'
}

// The members with no bot on surface, sorted: the loops that keep the page
// from offering an add there. A member the list of loops doesn't hold yet
// isn't named, since the page can't say what it lacks.
export function offSurface(
  members: readonly string[],
  loops: readonly LoopView[],
  surface: ChatSurface,
): string[] {
  const byName = new Map(loops.map((loop) => [loop.name, loop]))
  return members.filter((name) => {
    const loop = byName.get(name)
    return loop !== undefined && loopSurface(loop) !== surface
  })
}

// "@a", "@a and @b", "@a, @b and @c".
export function loopList(names: readonly string[]): string {
  const at = names.map((name) => `@${name}`)
  return at.length < 2 ? (at[0] ?? '') : `${at.slice(0, -1).join(', ')} and ${at[at.length - 1]}`
}

// The chat's name as the page shows it: its title, or `not seen yet` for a
// pasted id no member's bot has been in. A bot that was in it and found no
// title shows the id, which is all there is to tell it by.
export function chatName(chat: Pick<ChannelChat, 'title' | 'seen_at' | 'chat_id'>): string {
  if (chat.seen_at === null) return 'not seen yet'
  return chat.title || chat.chat_id
}

// What a member's status says, in the words of the chat's surface.
export function statusText(status: string, surface: string): string {
  const words = surfaceWords(surface)
  switch (status) {
    case 'carrying':
      return 'carrying'
    case 'not_in_chat':
      return `not in the ${words.chat}`
    case 'no_bot':
      return `no ${words.name} ${words.bot}`
    case 'unbound':
      return `in the ${words.chat}, not bound`
  }
  return 'not carrying'
}

// What a refused bind tells the operator. A refusal over members with no
// bot on the surface names them from the loops the page holds, since the
// hub's own sentence names them only in prose; any other refusal is the
// hub's sentence.
export function bindRefusal(
  err: unknown,
  channel: string,
  members: readonly string[],
  loops: readonly LoopView[],
  surface: ChatSurface,
): string {
  if (!(err instanceof ApiError)) return err instanceof Error ? err.message : String(err)
  const words = surfaceWords(surface)
  switch (err.code) {
    case 'members_not_on_surface': {
      const off = offSurface(members, loops, surface)
      if (off.length === 0) return err.message
      const has = off.length === 1 ? 'has' : 'have'
      return `Not bound: ${loopList(off)} ${has} no ${words.name} ${words.bot}. Add one on its page, or take it out of #${channel}.`
    }
    case 'room_in_use':
      return `Not bound: that ${words.chat} is another channel's. A ${words.chat} carries one channel.`
    case 'channel_has_chat':
      return `Not bound: #${channel} has a ${words.chat} already. Unbind it first.`
    case 'room_id_invalid':
      return `Not bound: ${err.message}`
  }
  return err.message
}
