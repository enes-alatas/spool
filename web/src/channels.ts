import { ApiError, type ChatMessage } from './api'

// The rules the Channels page checks before it asks the hub, mirrored from
// internal/store (ValidChannelName) and internal/httpapi (the description
// limit), so a form can say what is wrong while it is being typed. The hub
// still decides; a refusal from it is shown as it came.

// The fleet channel's name on the wire. It always exists, can't be created
// or deleted, and holds every loop the operator hasn't taken out of it.
export const FLEET_CHANNEL = 'group'

// Where a channel's chat opens: its own page under Channels (#1443). Only
// the fleet channel's chat opens so far.
export function channelChatPath(name: string): string {
  return `/channels/${name}`
}

export const CHANNEL_NAME_MAX = 32
export const CHANNEL_DESCRIPTION_MAX = 280

// 1–32 of a-z, 0-9 and '-', not starting with '-'. No underscore, so a
// channel can never be spelled like a private destination (owner_dm).
export function validChannelName(name: string): boolean {
  return /^[a-z0-9][a-z0-9-]{0,31}$/.test(name)
}

// The hub counts characters, not UTF-16 units: an emoji is one of 280, as
// the operator would count it, not two.
export function descriptionLength(description: string): number {
  return [...description].length
}

// A channel as the control room names it to the operator: the fleet channel
// by what it is, never by its wire name, and any other as #name.
export function channelLabel(name: string): string {
  return name === FLEET_CHANNEL ? 'fleet channel' : `#${name}`
}

// The loops a channel could take, in the order the pick-list offers them. A
// team's channel takes only that team's loops (ADR-0066), so no other is
// offered for the hub to refuse.
export function loopsOutside(
  channel: { loops: string[]; team: string },
  loops: { name: string; team: string }[],
): string[] {
  return loops
    .filter((loop) => !channel.loops.includes(loop.name) && (!channel.team || loop.team === channel.team))
    .map((loop) => loop.name)
    .sort()
}

// The loops in a channel that aren't in a team: what keeps the channel from
// being given to it, which the hub refuses naming them.
export function loopsNotInTeam(
  members: string[],
  team: string,
  loops: { name: string; team: string }[],
): string[] {
  const teamOf = new Map(loops.map((loop) => [loop.name, loop.team]))
  return members.filter((member) => teamOf.get(member) !== team).sort()
}

// Whose a channel is, as the control room says it.
export function channelOwner(team: string): string {
  return team ? `team ${team}` : 'hub channel'
}

// A loop list as a sentence names it: "@a", "@a and @b", "@a, @b and @c".
export function loopList(loops: string[]): string {
  const named = loops.map((loop) => `@${loop}`)
  return named.length < 2 ? named.join('') : `${named.slice(0, -1).join(', ')} and ${named[named.length - 1]}`
}

// A refusal from the hub about a channel's team, in words that name what to
// do (ADR-0066). The hub's own message is kept for any other. team is the
// team the channel is, or is being given to; home the team it is home to.
export function channelRefusal(
  err: unknown,
  at: { channel: string; team: string; home?: string; loop?: string; outsiders?: string[] },
): string {
  const message = err instanceof Error ? err.message : String(err)
  if (!(err instanceof ApiError)) return message
  switch (err.code) {
    case 'channel_in_other_team':
      return at.loop
        ? `#${at.channel} is team ${at.team}'s channel, and @${at.loop} isn't in team ${at.team}. Put it in the team first.`
        : message
    case 'channel_has_outsiders':
      return at.outsiders?.length
        ? `${loopList(at.outsiders)} ${at.outsiders.length === 1 ? "isn't" : "aren't"} in team ${at.team}. Take ${at.outsiders.length === 1 ? 'it' : 'them'} out of #${at.channel} first, then give the channel to the team.`
        : message
    case 'channel_is_home':
      return at.home
        ? `#${at.channel} is team ${at.home}'s home channel. Give the team another home channel, or none, first.`
        : message
    default:
      return message
  }
}

// Where a message was said, as a loop names it to send there (store
// Message.Destination): a channel besides the fleet channel as
// channel:<name>, and the fleet channel, the control room and owner DMs by
// their kind.
export function whereSaid(m: Pick<ChatMessage, 'conversation' | 'channel' | 'origin'>): string {
  if (m.conversation === 'group' && m.channel && m.channel !== FLEET_CHANNEL) return `channel:${m.channel}`
  return m.conversation || m.origin
}

// A channel card's anchor on the Channels page, which a team create lands
// on (#1266): "#channel-<name>", a channel name being URL-safe already.
export function channelAnchor(name: string): string {
  return `channel-${name}`
}

export function channelHash(name: string): string {
  return `#${channelAnchor(name)}`
}
