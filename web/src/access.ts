// Settings → Access (#732): who may reach the loops from Telegram and Slack
// now that a link is the gate (ADR-0051). A user reaches them through their
// links; a sender allowed before links keeps working, linked to nobody; and
// anyone else is a stranger the hub ignores, counted for a day.

import type { IgnoredStrangers, LoopView, SlackSender, TGSender, UserLink } from './api'

// A sender no user holds: one allowed, or blocked, or left waiting, by the
// pairing the hub had before links (#731). owns is the loops they are still
// the owner of.
export interface LegacySender {
  key: string
  surface: UserLink['surface']
  name: string
  id: string
  status: TGSender['status']
  owns: string[]
}

// The senders from before links, Telegram's first: every sender row with no
// user.
export function legacySenders(telegram: TGSender[], slack: SlackSender[], loops: LoopView[]): LegacySender[] {
  const fromTelegram = telegram
    .filter((s) => s.user === '')
    .map((s) => ({
      key: `telegram:${s.tg_user_id}`,
      surface: 'telegram' as const,
      name: s.username ? `@${s.username}` : s.display || String(s.tg_user_id),
      id: String(s.tg_user_id),
      status: s.status,
      owns: loops.filter((l) => l.owner_tg_user_id === s.tg_user_id).map((l) => l.name),
    }))
  const fromSlack = slack
    .filter((s) => s.user === '')
    .map((s) => ({
      key: `slack:${s.team_id}/${s.slack_user_id}`,
      surface: 'slack' as const,
      name: s.username ? `@${s.username}` : s.display || s.slack_user_id,
      id: s.slack_user_id,
      status: s.status,
      owns: loops.filter((l) => l.owner_slack_user_id === s.slack_user_id).map((l) => l.name),
    }))
  return [...fromTelegram, ...fromSlack]
}

const LABEL: Record<UserLink['surface'], string> = { telegram: 'Telegram', slack: 'Slack' }

// How many ids of a surface the line names before it says how many more:
// the hub keeps up to a thousand, and the line is for spotting a friend.
const NAMED = 5

// "3 unknown senders ignored in 24h: Telegram 700000011, 700000012 · Slack
// U0FIXTURE9." A surface past the hub's cap says "more than".
export function ignoredLine(strangers: IgnoredStrangers): string {
  const surfaces = strangers.surfaces.filter((s) => s.count > 0)
  const window = `${strangers.window_hours}h`
  if (surfaces.length === 0) return `No unknown sender messaged a bot in the last ${window}.`
  const total = surfaces.reduce((n, s) => n + s.count, 0)
  const more = surfaces.some((s) => s.more)
  const ids = surfaces
    .map((s) => {
      const named = s.ids.slice(0, NAMED).join(', ')
      const rest = s.count - NAMED
      return `${LABEL[s.surface]} ${named}${rest > 0 ? ` and ${rest} more` : ''}`
    })
    .join(' · ')
  return `${more ? 'More than ' : ''}${total} unknown ${total === 1 && !more ? 'sender' : 'senders'} ignored in ${window}: ${ids}.`
}

// While strangers' wrong codes have paused a surface, nobody new links
// there, a user sending their first code included; whoever is linked
// already is not touched (#789). Local time, as every clock in the room is.
export function pauseLine(surface: UserLink['surface'], until: number): string {
  const time = new Date(until).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
  return `Linking on ${LABEL[surface]} is paused until ${time}: strangers sent too many wrong codes. Anyone linked already is not affected.`
}
