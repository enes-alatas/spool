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

// The loops a channel could take, in the order the pick-list offers them.
export function loopsOutside(members: string[], loops: string[]): string[] {
  return loops.filter((loop) => !members.includes(loop)).sort()
}
