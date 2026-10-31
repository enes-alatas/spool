// A cloud loop (ADR-0056, #867): it runs in a Claude Code cloud session on
// Anthropic's machines, so it has no workstation of this hub's to power, and
// the hub holds it while its public address fails.

import { useQuery } from '@tanstack/react-query'
import {
  api,
  type CloudLoopSetup,
  type CloudLoopStep,
  type CloudSettings,
  type CloudTurn,
  type LoopView,
} from './api'
import type { Tone } from './cloud'
import { unmarked } from './marked'

type CloudLoop = Pick<LoopView, 'workstation_up' | 'down_reason' | 'workstation_detail' | 'problems'>

// Why the hub holds the loop, so that no turn starts, or '' when it
// doesn't (ADR-0056, as #864 and ADR-0061 amend it). A failed address
// check opens the `cloud_address` problem on the loop, and the loop's own
// setup not done (its repo, environment or hub token unset, or no check
// passed on them as they are now) opens `cloud_setup`. The rest open
// nothing, so they are read from Settings → Cloud, which the hub holds
// every cloud loop on: no address, an address that hasn't passed a check
// (one changed since its pass included), and no account signed in. A
// signed-in account whose check fails doesn't hold the loop, and neither
// does an unconfirmed allowlist, which is a warning (ADR-0059).
// Until Settings → Cloud answers, only the problems count.
export function cloudHold(loop: Pick<LoopView, 'problems'>, cloud: CloudSettings | undefined): string {
  const open = (symptom: string) => loop.problems.some((problem) => problem.symptom === symptom)
  if (open('cloud_address')) return 'the public address fails its check'
  if (cloud) {
    if (!cloud.address) return 'no public address is set'
    if (cloud.check?.state === 'failed') return 'the public address fails its check'
    if (cloud.check?.state !== 'ok') return "the public address hasn't passed a check yet"
    if (!cloud.account.set) return 'no Claude account is signed in'
  }
  if (open('cloud_setup')) return "the loop's setup isn't finished"
  return ''
}

// Where a hold is fixed: Settings → Cloud for the hub's, the loop's own
// setup for its own.
export function holdIsLoops(hold: string): boolean {
  return hold === "the loop's setup isn't finished"
}

// The hold a note beside the loop's name has to say: one no problem line
// already says.
export function unnamedCloudHold(loop: Pick<LoopView, 'problems'>, cloud: CloudSettings | undefined): string {
  if (
    loop.problems.some((problem) => problem.symptom === 'cloud_address' || problem.symptom === 'cloud_setup')
  )
    return ''
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
// or in the loop's own setup (#1009).
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

// What a cloud loop's setup check says (ADR-0061, #1182): its state as the
// card and the walk show it, and on a failure where it stopped and what to
// try. A pass of an earlier setup or address holds the loop as no check
// does, so it asks for another.
export function checkSays(
  setup: Pick<CloudLoopSetup, 'check' | 'secret_host'>,
  repo: string,
): { text: string; tone: Tone; hint?: string } {
  const { check } = setup
  const host = setup.secret_host || 'the public address'
  if (!check) return { text: 'not checked yet', tone: 'todo' }
  if (check.state === 'running') return { text: 'checking…', tone: 'todo' }
  if (check.state === 'ok' && !check.current)
    return {
      text: 'passed on an earlier setup',
      tone: 'warn',
      hint: 'The repo, environment, token or public address changed since this check passed. Check again.',
    }
  if (check.state === 'ok')
    return {
      text: 'passed',
      tone: 'ok',
      hint: `A session in the environment reached this hub. The loop can work in ${repo || 'its repo'}, push branches and open pull requests.`,
    }
  switch (check.stage) {
    case 'environment':
      return {
        text: 'fails: claude.ai refused the start',
        tone: 'bad',
        hint: "claude.ai wouldn't start a session there. Check that the environment is on the default Claude login's account, that the repo and branch exist, and that Claude's GitHub App is installed on the repo.",
      }
    case 'token':
      return {
        text: "fails: the session didn't carry the loop's token",
        tone: 'bad',
        hint: `A session reached the hub without the loop's token. Set the token as the environment's network secret for ${host}, or rotate it and set the new one.`,
      }
    case 'unreached':
      return {
        text: 'fails: nothing reached the hub',
        tone: 'bad',
        hint: `The hub heard nothing from the session. Check that ${host} is in the environment's allowed domains and that its setup script is the one shown here.`,
      }
    default:
      return { text: `fails: ${check.stage || 'unknown'}`, tone: 'bad' }
  }
}

// What the loop's setup still needs, for its card: '' once it is done.
const STEP_SAID: Record<CloudLoopStep, string> = {
  settings: 'Settings → Cloud',
  repo: 'a repo',
  environment: 'an environment',
  token: 'a hub token',
  allowlist: 'the allowed domains confirmed',
  check: 'a passed check',
}

export function stepsLeft(steps: CloudLoopStep[]): string {
  return steps.map((step) => STEP_SAID[step] ?? step).join(', ')
}

// The further repos a field holds, one a line or comma-separated, trimmed.
export function reposOf(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((repo) => repo.trim())
    .filter(Boolean)
}
