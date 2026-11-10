import type { LoopView, Team } from './api'

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
