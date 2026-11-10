import type { Room } from './api'

// Where adding a loop's bot to a Telegram group stands (#1319), from the
// groups the loop had when the steps opened and the groups it has now.
// The group that arrived is the first one not known before. Until one
// arrives, the operator is on the first two steps: adding the bot, and
// sending /spool_status from a linked account. Every group arrives unbound
// (ADR-0068), so the third step is always picking the channel it carries.
export interface GroupProgress {
  arrived?: Room
  // Steps done, of three.
  done: number
}

export function groupProgress(known: ReadonlySet<string>, rooms: readonly Room[]): GroupProgress {
  const arrived = rooms.find((room) => !known.has(room.room_id))
  const done = !arrived ? 0 : arrived.channel ? 3 : 2
  return { arrived, done }
}
