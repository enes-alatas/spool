// What putting a loop in a channel does in the channel's chat (ADR-0070,
// #1511): the loop's room there binds by itself, so the loop starts talking
// in a Telegram group or Slack channel no one touched. The membership
// controls say so before the write, and word the hub's refusal of a loop
// with no bot on the chat's surface.
import { ApiError, type ChannelChat, type LoopView } from './api'
import { loopSurface } from './slack'
import { chatName, chatSurface, surfaceWords } from './surfaceChat'

// What a control says before it puts loop in channel, whose chat is chat:
// `ask` when the loop will carry the channel there, so the write waits for
// a yes; `refuse` when the loop has no bot on the chat's surface, which the
// hub would refuse. null when the channel has no chat: nothing to say.
// Either way the note keeps the chat it was read from, so a refusal of the
// write that follows names the same chat.
export interface JoinNote {
  kind: 'ask' | 'refuse'
  text: string
  chat: ChannelChat
}

export function joinNote(
  channel: string,
  chat: ChannelChat | null,
  loop: Pick<LoopView, 'name' | 'surface' | 'has_tg_token'> | undefined,
  lead = '',
): JoinNote | null {
  if (!chat || !loop) return null
  const surface = chatSurface(chat.surface)
  const words = surfaceWords(surface)
  const where = chatWhere(chat)
  if (loopSurface(loop) !== surface) {
    return { kind: 'refuse', text: noBotText(loop.name, channel, words.name, where), chat }
  }
  return { kind: 'ask', text: `${lead}@${loop.name} will carry #${channel} in ${where}.`, chat }
}

// The hub's 409 loop_not_on_surface for loop joining channel, in the
// control's words, naming the chat the control read before the write. null
// for any other error, and with no chat at hand, since the code doesn't say
// which surface: the hub's own sentence is shown then.
export function notOnSurfaceText(
  err: unknown,
  channel: string,
  loop: string,
  chat: ChannelChat | undefined,
): string | null {
  if (!(err instanceof ApiError) || err.code !== 'loop_not_on_surface' || !chat) return null
  return noBotText(loop, channel, surfaceWords(chatSurface(chat.surface)).name, chatWhere(chat))
}

function noBotText(loop: string, channel: string, surface: string, where: string): string {
  return `@${loop} has no ${surface} ${surface === 'Slack' ? 'app' : 'bot'}, and #${channel} is carried in ${where}. Give @${loop} one first.`
}

// "the Telegram group “Release chat”", or by its id while no member's bot
// has been in it.
export function chatWhere(chat: ChannelChat): string {
  const words = surfaceWords(chatSurface(chat.surface))
  const name = chat.seen_at === null ? chat.chat_id : `“${chatName(chat)}”`
  return `the ${words.name} ${words.chat} ${name}`
}
