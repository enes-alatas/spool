// A loop's health (ADR-0053): the problems the hub's health check found
// with it, said in the room.

import type { Health, Problem } from './api'

// How long a problem has been open, "40m", "2h", "3d", from since to now.
export function openFor(since: number, now: number): string {
  const minutes = Math.floor(Math.max(0, now - since) / 60000)
  if (minutes < 1) return 'under a minute'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

// What each symptom is, as the start of its line. A symptom this build does
// not know is still a problem the hub opened, and is said by its name.
const SAID: Record<string, string> = {
  workstation_down: 'Workstation down',
  turn_errors: 'Turns failing',
  // a turn still running past health_turn_minutes (#1142); its since is
  // the turn's start, so its line says when that was
  turn_stuck: 'Turn past its time limit',
  // a wake owed past health_wake_grace_minutes with no turn started (#1143);
  // its since is when the wake was first owed
  wake_missed: "Due to wake and hasn't",
  // a send the loop made left undelivered past health_send_grace_minutes,
  // with nothing resolving it (#1144); its since is the earliest such
  // failure
  send_failed: 'Send failed',
  // a cloud loop held while the public address fails its check (#864)
  cloud_address: 'Public address failing',
  // a cloud loop held until its own setup is done: its repo, environment
  // and hub token, and a check passed on them (ADR-0061)
  cloud_setup: 'Needs setup',
  // a cloud loop's session refused its connections, so its shell may run
  // without them (ADR-0059); said as what the loop lacks, a warning, as the
  // loop still runs
  cloud_connections: 'Running without its secrets',
}

// What a symptom means and what to do, where the label alone leaves the
// operator asking; a symptom without one has no tooltip.
const MEANS: Record<string, string> = {
  turn_stuck:
    'A turn has run longer than the limit set under Health check in Settings. The hub never ends a ' +
    'turn for running long: Kill process ends it, which clears this. Acknowledge clears it and lets the ' +
    'turn run on.',
  wake_missed:
    'The loop was due to wake, from a tick or a message, and no turn has started though nothing holds ' +
    'it, so its process may have stopped answering. A turn starting clears this. Acknowledge clears it ' +
    'and starts the wait over.',
  send_failed:
    'A message the loop sent did not arrive, and no retry, resend or dismissal has dealt with it. The ' +
    'loop is woken once to hear so. Resending or dismissing every one under Undelivered clears this. ' +
    'Acknowledge clears it and starts the wait over.',
  cloud_connections:
    'Its cloud session asked the hub for its secrets, the env variables attached to it on the ' +
    'Connections page (like GH_TOKEN), and was refused, so commands that need them may fail. ' +
    'Pause and resume the loop to hand them over again.',
}

export function problemMeaning(problem: Problem): string | undefined {
  return MEANS[problem.symptom]
}

// "Workstation down for 2h". A turn past its limit says when the turn
// started as well, since that is the turn the owner is deciding about:
// "Turn past its time limit: running for 2h, since 14:02". A missed wake
// says when the wake fell due: "Due to wake and hasn't: waiting 25m, since
// 14:02". A failed send says when the first one failed: "Send failed:
// undelivered for 25m, since 14:02".
export function problemLine(problem: Problem, now: number): string {
  const said = SAID[problem.symptom] ?? problem.symptom
  const doing = CLOCKED[problem.symptom]
  if (doing) return `${said}: ${doing} ${openFor(problem.since, now)}, since ${clockAt(problem.since, now)}`
  return `${said} for ${openFor(problem.since, now)}`
}

// The symptoms whose line names its start as a clock time, and what has
// gone on since.
const CLOCKED: Record<string, string> = {
  turn_stuck: 'running for',
  wake_missed: 'waiting',
  send_failed: 'undelivered for',
}

// What the loop page offers beside a problem's line, where the way out is a
// control elsewhere on the page rather than Acknowledge: the hub never ends
// a long turn itself, so the owner is pointed at the one control that does,
// and an undelivered message at the pane that lists it.
export function problemNext(problem: Problem): string | undefined {
  return NEXT[problem.symptom]
}

const NEXT: Record<string, string> = {
  turn_stuck: 'Kill process ends it',
  send_failed: 'Resend or dismiss under Undelivered',
}

// A moment as a clock time, with the date as well when it is not today: a
// turn's limit runs up to a day, so its start can be yesterday's.
function clockAt(at: number, now: number): string {
  const d = new Date(at)
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (d.toDateString() === new Date(now).toDateString()) return time
  return `${d.toLocaleDateString([], { month: 'short', day: 'numeric' })}, ${time}`
}

// The Fleet row's note: every open problem, "workstation down for 2h ·
// turns failing for 40m". Only each line's first letter is lowered: a turn
// past its limit carries a clock time, which keeps the locale's "PM".
export function healthNote(problems: Problem[], now: number): string {
  return problems
    .map((problem) => {
      const line = problemLine(problem, now)
      return line.charAt(0).toLowerCase() + line.slice(1)
    })
    .join(' · ')
}

// A problem's line takes its loop's colour: a workstation down, or a cloud
// loop held by its public address or its own setup, stops the loop, so it
// is the alarm, as is a wake that starts no turn, since the loop is doing
// nothing; failing turns, or one running long, are a warning.
export function problemHealth(problem: Problem): Health {
  return HOLDS.includes(problem.symptom) ? 'failing' : 'warning'
}

const HOLDS = ['workstation_down', 'wake_missed', 'cloud_address', 'cloud_setup']

// The error-turn threshold as Settings edits it: '' for off.
export function errorTurnsField(turns: number): string {
  return turns > 0 ? String(turns) : ''
}

// The field back as a count to save, or undefined when it is not one the
// hub takes (0 to 100, empty for off).
export function errorTurnsValue(field: string): number | undefined {
  const text = field.trim()
  if (text === '') return 0
  if (!/^\d+$/.test(text)) return undefined
  const turns = Number(text)
  return turns <= 100 ? turns : undefined
}

// A health limit in minutes, turn_stuck's, wake_missed's or send_failed's, as Settings
// edits it: '' for off.
export function minutesField(minutes: number): string {
  return minutes > 0 ? String(minutes) : ''
}

// The field back as minutes to save, or undefined when it is not a limit the
// hub takes (0 to 1440, a day, empty for off).
export function minutesValue(field: string): number | undefined {
  const text = field.trim()
  if (text === '') return 0
  if (!/^\d+$/.test(text)) return undefined
  const minutes = Number(text)
  return minutes <= 1440 ? minutes : undefined
}

// The steps a health setting's slider snaps to, 0 (off) first. Dragging is
// coarse on purpose: a day's worth of minutes on one track would make the
// common limits a pixel apart. An exact value is typed into the number
// above the slider instead.
export const ERROR_TURN_STOPS = [0, 1, 2, 3, 4, 5, 6, 8, 10, 15, 20, 50, 100]
export const TURN_MINUTE_STOPS = [0, 5, 15, 30, 60, 90, 120, 180, 240, 360, 480, 720, 1440]
export const WAKE_MINUTE_STOPS = [0, 1, 2, 5, 10, 15, 30, 60, 120, 240, 480, 1440]
export const SEND_MINUTE_STOPS = [0, 1, 2, 3, 5, 10, 15, 30, 60, 120, 240, 1440]

// Where a value sits on its slider: the last step at or below it, so a
// typed value between two steps puts the thumb on the lower one.
export function stopIndex(stops: number[], value: number): number {
  let index = 0
  stops.forEach((stop, i) => {
    if (stop <= value) index = i
  })
  return index
}

// The number field after ↑ or ↓: one up or down from what it holds, kept to
// 0..max, '' for off. A field that holds no number yet steps from 0.
export function stepField(field: string, delta: number, max: number): string {
  const current = /^\d+$/.test(field.trim()) ? Number(field.trim()) : 0
  const next = Math.min(max, Math.max(0, current + delta))
  return next > 0 ? String(next) : ''
}

// Minutes as a reader takes them in at a glance, beside the exact number:
// "2h", "1h 30m", "1d"; '' under an hour, where the number says it.
export function minutesNote(minutes: number): string {
  if (minutes < 60) return ''
  if (minutes % 1440 === 0) return `${minutes / 1440}d`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest ? `${hours}h ${rest}m` : `${hours}h`
}

// A step's label under the slider: "off", "15m", "2h", "1d", or a count.
export function stopLabel(stop: number, minutes: boolean): string {
  if (stop === 0) return 'off'
  if (!minutes) return String(stop)
  return minutesNote(stop) || `${stop}m`
}
