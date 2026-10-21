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
  // a cloud loop held while the public address fails its check (#864)
  cloud_address: 'Public address failing',
  // a cloud loop's session refused its connections, so its shell may run
  // without them (ADR-0059); said as what the loop lacks, a warning, as the
  // loop still runs
  cloud_connections: 'Running without its secrets',
}

// What a symptom means and what to do, where the label alone leaves the
// operator asking; a symptom without one has no tooltip.
const MEANS: Record<string, string> = {
  cloud_connections:
    'Its cloud session asked the hub for its secrets, the env variables attached to it on the ' +
    'Connections page (like GH_TOKEN), and was refused, so commands that need them may fail. ' +
    'Pause and resume the loop to hand them over again.',
}

export function problemMeaning(problem: Problem): string | undefined {
  return MEANS[problem.symptom]
}

// "Workstation down for 2h".
export function problemLine(problem: Problem, now: number): string {
  return `${SAID[problem.symptom] ?? problem.symptom} for ${openFor(problem.since, now)}`
}

// The Fleet row's note: every open problem, "workstation down for 2h ·
// turns failing for 40m".
export function healthNote(problems: Problem[], now: number): string {
  return problems.map((problem) => problemLine(problem, now).toLowerCase()).join(' · ')
}

// A problem's line takes its loop's colour: a workstation down, or a cloud
// loop held by its public address, stops the loop, so it is the alarm;
// failing turns are a warning.
export function problemHealth(problem: Problem): Health {
  return problem.symptom === 'workstation_down' || problem.symptom === 'cloud_address' ? 'failing' : 'warning'
}

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
