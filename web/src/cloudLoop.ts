// A cloud loop (ADR-0056, #867): it runs in a Claude Code cloud session on
// Anthropic's machines, so it has no workstation of this hub's to power, and
// the hub holds it while its public address fails.

import { useQuery } from '@tanstack/react-query'
import { api, type CloudSettings, type LoopView } from './api'

type CloudLoop = Pick<LoopView, 'workstation_up' | 'down_reason' | 'workstation_detail' | 'problems'>

// Why the hub holds the loop, so that no turn starts, or '' when it
// doesn't (ADR-0056, as #864 amends it). A failed address check
// opens the `cloud_address` problem on the loop. The rest open nothing, so
// they are read from Settings → Cloud, which the hub holds every cloud loop
// on: no address, an address that hasn't passed a check (one changed since
// its pass included), and no account signed in. A signed-in account whose
// check fails doesn't hold the loop. Until Settings → Cloud answers, only
// the problem counts.
export function cloudHold(loop: Pick<LoopView, 'problems'>, cloud: CloudSettings | undefined): string {
  if (loop.problems.some((problem) => problem.symptom === 'cloud_address'))
    return 'the public address fails its check'
  if (!cloud) return ''
  if (!cloud.address) return 'no public address is set'
  if (cloud.check?.state === 'failed') return 'the public address fails its check'
  if (cloud.check?.state !== 'ok') return "the public address hasn't passed a check yet"
  if (!cloud.account.set) return 'no Claude account is signed in'
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

// The cloud loop's session as the Workstation card says it. Held outranks
// the rest: a session that is up still takes no turn while held.
export function cloudSessionCondition(loop: CloudLoop, hold: string): { status: string; alert: boolean } {
  if (hold) return { status: 'held', alert: true }
  if (loop.workstation_up) return { status: 'ready', alert: false }
  // the cloud environment is made at the loop's first wake
  if (loop.down_reason === 'not_provisioned') return { status: 'no session yet', alert: false }
  return { status: loop.workstation_detail ? `down: ${loop.workstation_detail}` : 'down', alert: true }
}

// GitHub's owner and repo name rules over HTTPS, as the hub reads
// `cloud_repo` (internal/httpapi/cloud_loops.go).
const REPO = /^https:\/\/github\.com\/[A-Za-z0-9][A-Za-z0-9-]{0,38}\/[A-Za-z0-9._-]{1,100}$/

// Why a repo the form was given won't be taken, or '' when it will. A
// trailing slash or .git, as a clone URL has, is dropped first, as the hub
// drops it.
export function cloudRepoError(raw: string): string {
  let repo = raw.trim()
  if (repo.endsWith('/')) repo = repo.slice(0, -1)
  if (repo.endsWith('.git')) repo = repo.slice(0, -4)
  if (repo === '') return ''
  const name = repo.slice(repo.lastIndexOf('/') + 1)
  if (!REPO.test(repo) || name === '.' || name === '..')
    return 'A GitHub repo, as https://github.com/<owner>/<repo>.'
  return ''
}
