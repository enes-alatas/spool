import { ApiError, type Channel, type LoopView, type Team } from './api'
import { validChannelName } from './channels'

// The rules the Fleet page's team cards check before it asks the hub, mirrored from the
// hub (a team's name follows the loop-name rule, ADR-0066), so a form can
// say what is wrong while it is being typed. The hub still decides; a
// refusal from it is shown as it came.

export const TEAM_NAME_MAX = 32
export const TEAM_DESCRIPTION_MAX = 280

// 2–32 of a-z, 0-9, '_' and '-', starting with a letter or digit: the
// loop-name rule.
export function validTeamName(name: string): boolean {
  return /^[a-z0-9][a-z0-9_-]{1,31}$/.test(name)
}

// Why a delete can't go ahead, said before asking the hub, which refuses it
// the same way (409 team_has_loops): '' when the team is empty.
export function teamDeleteRefusal(team: Pick<Team, 'name' | 'loops'>): string {
  if (team.loops.length === 0) return ''
  const names = team.loops.map((loop) => `@${loop}`).join(', ')
  return `${team.name} still has ${names}. Move ${team.loops.length === 1 ? 'it' : 'them'} out first.`
}

// The loops a team could take, in the order its pick-list offers them, each
// with the team it would leave: a loop is in one team at most, so adding one
// moves it.
export function loopsToAdd(
  team: Pick<Team, 'name'>,
  loops: Pick<LoopView, 'name' | 'team'>[],
): { name: string; from: string }[] {
  return loops
    .filter((loop) => loop.team !== team.name)
    .map((loop) => ({ name: loop.name, from: loop.team }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

// Where a loop on the card `from` can be moved (#1485), in the cards'
// order: each team, then no team, with the card it is on marked as such
// rather than left out, so the list says where the loop is now. A card's key
// is its team's name, '' for the loops in none.
export function moveTargets(
  teams: Pick<Team, 'name'>[],
  from: string,
): { key: string; label: string; current: boolean }[] {
  return [...teams.map((team) => team.name), ''].map((key) => ({
    key,
    label: key || 'No team',
    current: key === from,
  }))
}

// A loop's team as the room names it.
export function teamLabel(team: string): string {
  return team || 'no team'
}

// Whether something is wrong with a loop, whatever it is: a problem the hub
// reports, a message that never arrived, or a workstation that is down. The
// Fleet page draws every one of them the same red (#1411).
export function needsYou(loop: Pick<LoopView, 'problems' | 'undelivered' | 'state'>): boolean {
  return loop.problems.length > 0 || loop.undelivered > 0 || loop.state === 'workstation_down'
}

// A team's card on the Fleet page, or the card of the loops in none (key '').
export interface TeamGroup {
  key: string
  team?: Team
  loops: LoopView[]
}

// The cards, in the hub's team order with each team's loops by name, and the
// loops in no team last. The order holds still as faults come and go, so a
// loop is where it was a minute ago.
export function teamGroups(teams: Team[], loops: LoopView[]): TeamGroup[] {
  const byName = (a: LoopView, b: LoopView) => a.name.localeCompare(b.name)
  const groups: TeamGroup[] = teams.map((team) => ({
    key: team.name,
    team,
    loops: loops.filter((loop) => loop.team === team.name).sort(byName),
  }))
  const loose = loops.filter((loop) => !loop.team || !teams.some((team) => team.name === loop.team))
  if (loose.length > 0) groups.push({ key: '', loops: loose.sort(byName) })
  return groups
}

// How the Fleet page lays the fleet out: as its teams' cards, or as the rows
// it showed before teams (#1411). One browser's choice, kept in its storage.
// Until a browser makes one, the hub's teams decide (#1484): the cards once
// there is a team, the rows while there is none.
export type FleetLayout = 'teams' | 'list'
const FLEET_LAYOUT_KEY = 'spool.fleet.layout'

export function readFleetLayout(): FleetLayout | undefined {
  try {
    const saved = globalThis.localStorage?.getItem(FLEET_LAYOUT_KEY)
    return saved === 'list' || saved === 'teams' ? saved : undefined
  } catch {
    return undefined
  }
}

export function defaultFleetLayout(teams: readonly unknown[]): FleetLayout {
  return teams.length > 0 ? 'teams' : 'list'
}

export function saveFleetLayout(layout: FleetLayout) {
  try {
    globalThis.localStorage?.setItem(FLEET_LAYOUT_KEY, layout)
  } catch {
    // the choice holds for this visit only
  }
}

// A new team's home channel (#1494): the hub makes one with the team, named
// after it unless the form names another. A channel name has no underscore,
// so the team's are read as dashes.
export function suggestedHomeChannel(team: string): string {
  return team.replace(/_/g, '-')
}

// The fleet channel's old name, which the hub never lets a channel take
// again (ADR-0068, store.RetiredChannel).
export const RETIRED_CHANNEL = 'group'

// Why the hub would refuse a new team's home channel, in the form's words,
// or '' when it wouldn't. The name taken by another channel is checked
// against the channels the room has, so it is said before the create.
export function homeChannelProblem(home: string, channels: Pick<Channel, 'name'>[] | undefined): string {
  if (home === '') return ''
  if (home === RETIRED_CHANNEL)
    return `No channel can be named ${RETIRED_CHANNEL}. Give this team's home channel another name.`
  if (!validChannelName(home))
    return 'A channel name is 1 to 32 lowercase letters, digits and dashes, not starting with a dash.'
  if (channels?.some((channel) => channel.name === home))
    return `A channel named ${home} already exists. Give this team's home channel another name.`
  return ''
}

// What the New team form says at its home channel: the hub's refusal while
// the field still holds the name it refused, since the team's name it
// follows can move it on; that it is needed, once a name is typed and the
// field emptied by hand; else what the hub would refuse.
export function newHomeProblem(form: {
  name: string
  home: string
  refused: { home: string; text: string } | undefined
  channels: Pick<Channel, 'name'>[] | undefined
}): string {
  if (form.refused?.home === form.home) return form.refused.text
  if (form.name !== '' && form.home === '') return "Give this team's home channel a name."
  return homeChannelProblem(form.home, form.channels)
}

// The hub's refusal of a new team's home channel, in the form's words, or
// '' when the refusal is about something else. A channel made since the
// room last read the list is still caught here.
export function homeChannelRefusal(err: unknown, home: string): string {
  if (!(err instanceof ApiError)) return ''
  switch (err.code) {
    case 'channel_exists':
      return `A channel named ${home} already exists. Give this team's home channel another name.`
    case 'channel_reserved':
    case 'channel_name_invalid':
      return homeChannelProblem(home, []) || err.message
    default:
      return ''
  }
}
