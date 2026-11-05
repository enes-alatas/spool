import type { Room } from './api'
import { FLEET_CHANNEL } from './channels'

// Where adding a loop's bot to a Telegram group stands (#1319), from the
// groups the loop had when the steps opened and the groups it has now.
// The group that arrived is the first one not known before. Until one
// arrives, the operator is on the first two steps: adding the bot, and
// sending /spool_status from a linked account. Once one arrives, it either
// carries the fleet channel by itself, the first group a loop has, or
// waits for a channel to be picked.
export interface GroupProgress {
  arrived?: Room
  // Whether the group takes a channel the operator picks rather than the
  // fleet channel: the one arrived came unbound, or before one arrives, the
  // fleet channel already has its group, so the next one will.
  needsChannel: boolean
  // Steps done, of three.
  done: number
}

export function groupProgress(known: ReadonlySet<string>, rooms: readonly Room[]): GroupProgress {
  const arrived = rooms.find((room) => !known.has(room.room_id))
  const needsChannel = arrived
    ? arrived.channel !== FLEET_CHANNEL
    : rooms.some((room) => room.channel === FLEET_CHANNEL)
  const done = !arrived ? 0 : arrived.channel ? 3 : 2
  return { arrived, needsChannel, done }
}
