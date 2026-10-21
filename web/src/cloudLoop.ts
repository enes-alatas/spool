// A cloud loop (ADR-0056, #867): it runs in a Claude Code cloud session on
// Anthropic's machines, so it has no workstation of this hub's to power, and
// the hub holds it while its public address fails.

import { useQuery } from '@tanstack/react-query'
import { api, type CloudSettings, type LoopView } from './api'
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
