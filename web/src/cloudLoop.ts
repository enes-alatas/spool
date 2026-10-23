// A cloud loop (ADR-0056, #867): it runs in a Claude Code cloud session on
// Anthropic's machines, so it has no workstation of this hub's to power, and
// the hub holds it while its public address fails.

import { useQuery } from '@tanstack/react-query'
import { api, type CloudSettings, type CloudTurn, type LoopView } from './api'
import { unmarked } from './marked'

type CloudLoop = Pick<LoopView, 'workstation_up' | 'down_reason' | 'workstation_detail' | 'problems'>

// Why the hub holds the loop, so that no turn starts, or '' when it
// doesn't (ADR-0056, as #864 amends it). A failed address check
// opens the `cloud_address` problem on the loop. The rest open nothing, so
// they are read from Settings → Cloud, which the hub holds every cloud loop
// on: no address, an address that hasn't passed a check (one changed since
// its pass included), no account signed in, and no environment chosen. A
// signed-in account whose check fails doesn't hold the loop, and neither
// does an unconfirmed allowlist, which is a warning (ADR-0059).
// Until Settings → Cloud answers, only the problem counts.
export function cloudHold(loop: Pick<LoopView, 'problems'>, cloud: CloudSettings | undefined): string {
  if (loop.problems.some((problem) => problem.symptom === 'cloud_address'))
    return 'the public address fails its check'
  if (!cloud) return ''
  if (!cloud.address) return 'no public address is set'
  if (cloud.check?.state === 'failed') return 'the public address fails its check'
  if (cloud.check?.state !== 'ok') return "the public address hasn't passed a check yet"
  if (!cloud.account.set) return 'no Claude account is signed in'
  if (!cloud.environment.id) return 'no cloud environment is chosen'
  return ''
}

// The hold a note beside the loop's name has to say: one the `cloud_address`
// problem doesn't already say.
export function unnamedCloudHold(loop: Pick<LoopView, 'problems'>, cloud: CloudSettings | undefined): string {
  if (loop.problems.some((problem) => problem.symptom === 'cloud_address')) return ''
  return cloudHold(loop, cloud)
}

// Settings → Cloud, read for a cloud loop alone: every member may read it,
// and one answer serves every cloud loop on the page.
export function useCloudSettings(loop: Pick<LoopView, 'runtime'>): CloudSettings | undefined {
  return useQuery({ queryKey: ['cloud'], queryFn: api.cloudSettings, enabled: loop.runtime === 'cloud' }).data
}

// The cloud loop's session as the Workstation card says it. A hold outranks
// the rest, since a session that is up still takes no turn while held; the
// room calls it "needs setup", as every hold is a gap in Settings → Cloud
// (#1009).
export function cloudSessionCondition(loop: CloudLoop, hold: string): { status: string; alert: boolean } {
  if (hold) return { status: 'needs setup', alert: true }
  if (loop.workstation_up) return { status: 'ready', alert: false }
  // the cloud environment is made at the loop's first wake
  if (loop.down_reason === 'not_provisioned') return { status: 'no session yet', alert: false }
  return {
    status: loop.workstation_detail ? `down: ${unmarked(loop.workstation_detail)}` : 'down',
    alert: true,
  }
}

// What deleting the loop leaves behind, asked before it goes. A cloud loop
// has no worktree. Each new session it started (a pause and resume, a kill,
// a rotation) is still on claude.ai, holding the values of the env-var
// connections it was handed, until the operator archives it there. So a
// loop deleted for misbehaving names the ones attached now to rotate, and
// any detached since (ADR-0059).
export function deleteQuestion(loop: Pick<LoopView, 'name' | 'runtime' | 'connections'>): string {
  if (loop.runtime !== 'cloud')
    return `Delete loop @${loop.name}? Its worktree is removed; the branch is kept.`
  const attached = (loop.connections ?? []).filter((c) => c.kind === 'env-var').map((c) => c.name)
  const after = attached.length
    ? ` Its sessions on claude.ai keep the secrets they were handed until you archive them there. It has ${attached.join(', ')} attached now; rotate them, and any you detached, if the loop misbehaved.`
    : ' Archive its sessions on claude.ai once it is gone: they keep any secrets they were handed.'
  return `Delete loop @${loop.name}?${after}`
}

// The clock time a cloud turn stops being counted, "14:32".
export function turnUntil(turn: CloudTurn): string {
  return new Date(turn.until).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

// A turn Spool can no longer hear, cut off by a stop, a kill or a rotation,
// that may still run until its bound: the page says so, and where to
// interrupt it. A turn past its bound, or one the loop still hears, isn't.
export function cutOffTurn(loop: Pick<LoopView, 'cloud_turn'>, now: number): CloudTurn | undefined {
  const turn = loop.cloud_turn
  return turn?.cut_off && now < turn.until ? turn : undefined
}

// What the kill button does, in its hover line. A cloud loop's kill revokes
// its session's token, but a turn already running there runs on, cut off
// from the hub (ADR-0059), so its line doesn't promise the turn stops.
export function killHint(loop: Pick<LoopView, 'runtime'>): string {
  if (loop.runtime !== 'cloud')
    return 'Stops its claude process now, mid-turn if need be; it wakes again as scheduled.'
  return 'Cuts its session off from this hub now; a turn already running on claude.ai runs on. It wakes again as scheduled.'
}

// What the kill button asks first while a cloud turn may be running: a kill
// stops the hub's side, never the turn in the session (ADR-0059). undefined
// when there's nothing to warn of, and the kill goes ahead unasked.
export function killQuestion(loop: Pick<LoopView, 'name' | 'cloud_turn'>, now: number): string | undefined {
  const turn = loop.cloud_turn
  if (!turn || now >= turn.until) return undefined
  return (
    `Kill @${loop.name}? A turn may still be running in its session on claude.ai until ${turnUntil(turn)}, ` +
    `and Spool can't stop it. Open the session on claude.ai to interrupt it.`
  )
}
