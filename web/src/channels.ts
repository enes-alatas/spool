import type { ChatMessage } from './api'

// The rules the Channels page checks before it asks the hub, mirrored from
// internal/store (ValidChannelName) and internal/httpapi (the description
// limit), so a form can say what is wrong while it is being typed. The hub
// still decides; a refusal from it is shown as it came.

// The fleet channel's name on the wire. It always exists, can't be created
// or deleted, and holds every loop the operator hasn't taken out of it.
export const FLEET_CHANNEL = 'group'

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

// The loops a channel could take, in the order the pick-list offers them.
export function loopsOutside(members: string[], loops: string[]): string[] {
  return loops.filter((loop) => !members.includes(loop)).sort()
}

// Where a message was said, as a loop names it to send there (store
// Message.Destination): a channel besides the fleet channel as
// channel:<name>, and the fleet channel and the private conversations by
// their kind.
export function whereSaid(m: Pick<ChatMessage, 'conversation' | 'channel' | 'origin'>): string {
  if (m.conversation === 'group' && m.channel && m.channel !== FLEET_CHANNEL) return `channel:${m.channel}`
  return m.conversation || m.origin
}
