// The loops' change record (#695, #696): what each change says in the room.

import type { ChatMessage, LoopChange } from './api'

// What each action did, said twice: in a sentence that names the loop, for a
// list of every loop's changes, and without it, for the loop's own page,
// which names the loop already.
const SAID: Record<string, [(loop: string) => string, string]> = {
  create: [(loop) => `created ${loop}`, 'created'],
  edit: [(loop) => `edited ${loop}`, 'edited'],
  delete: [(loop) => `deleted ${loop}`, 'deleted'],
  pause: [(loop) => `paused ${loop}`, 'paused'],
  resume: [(loop) => `resumed ${loop}`, 'resumed'],
  wake: [(loop) => `woke ${loop}`, 'woken'],
  kill: [(loop) => `killed ${loop}'s process`, 'process killed'],
  rotate: [(loop) => `rotated ${loop}'s session`, 'session rotated'],
  rehome: [(loop) => `rehomed ${loop}`, 'rehomed'],
  cloud_token: [(loop) => `minted ${loop}'s hub token`, 'hub token minted'],
  cloud_allowlist: [(loop) => `confirmed ${loop}'s allowed domains`, 'allowed domains confirmed'],
  cloud_check: [(loop) => `checked ${loop}'s cloud setup`, 'cloud setup checked'],
  workstation_restart: [(loop) => `restarted ${loop}'s workstation`, 'workstation restarted'],
  workstation_poweroff: [(loop) => `powered off ${loop}'s workstation`, 'workstation powered off'],
  workstation_poweron: [(loop) => `powered on ${loop}'s workstation`, 'workstation powered on'],
  workstation_recreate: [(loop) => `recreated ${loop}'s workstation`, 'workstation recreated'],
  owner: [(loop) => `changed ${loop}'s owner`, 'owner changed'],
  room: [(loop) => `bound a room to ${loop}`, 'room bound'],
  unroom: [(loop) => `unbound a room from ${loop}`, 'room unbound'],
  attach: [(loop) => `attached a connection to ${loop}`, 'connection attached'],
  detach: [(loop) => `detached a connection from ${loop}`, 'connection detached'],
  join: [(loop) => `added ${loop} to a channel`, 'added to a channel'],
  leave: [(loop) => `took ${loop} out of a channel`, 'taken out of a channel'],
  acknowledge: [(loop) => `acknowledged a problem with ${loop}`, 'problem acknowledged'],
}

// "admin paused @gardener". An action this build does not know is still a
// real change, and is said by its name.
export function changeSentence(change: LoopChange): string {
  const loop = `@${change.loop}`
  const said = SAID[change.action]?.[0](loop) ?? `${change.action} ${loop}`
  return `${change.by} ${said}`
}

// "paused", on the loop's own page, where the row goes on to say by whom.
export function changeOnPage(action: string): string {
  return SAID[action]?.[1] ?? action
}

// Activity's feed with the change record woven in by time, newest first.
// Both lists are newest windows, so a change older than the oldest message
// shown is left out while the messages run to their limit: below it the
// feed holds no messages to place it among, and a run of changes alone
// would read as all that happened then.
export type FeedItem =
  { kind: 'message'; at: number; msg: ChatMessage } | { kind: 'change'; at: number; change: LoopChange }

export function interleave(msgs: ChatMessage[], changes: LoopChange[], full: boolean): FeedItem[] {
  const floor = full && msgs.length > 0 ? Math.min(...msgs.map((m) => m.ts)) : -Infinity
  const items: FeedItem[] = [
    ...msgs.map((msg): FeedItem => ({ kind: 'message', at: msg.ts, msg })),
    ...changes
      .filter((c) => c.at >= floor)
      .map((change): FeedItem => ({ kind: 'change', at: change.at, change })),
  ]
  return items.sort((a, b) => b.at - a.at)
}
