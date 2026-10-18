import type { CloudEnvironmentSync, EgressView } from './api'
import { formatAgo } from './format'

// Whether a change to the list is still on its way: egress is walled, and
// the proxy last loaded a list older than the current one. No
// applied_at means the proxy doesn't hold the current list and waits for
// the next docker wake, so there is nothing to poll for.
export function applying(view?: EgressView): boolean {
  return (
    !!view?.enforced &&
    view.changed_at !== undefined &&
    view.applied_at !== undefined &&
    view.applied_at < view.changed_at
  )
}

// Whether a cloud loop's environment carries the allowlist as it stands
// (ADR-0056), in the words the Egress section and the loop's Workstation
// card both use. The error is an admin's alone, so a member reads only that
// it isn't in sync.
export function allowlistSyncText(environment: CloudEnvironmentSync, now: number): string {
  if (environment.in_sync)
    return `in sync${environment.synced_at ? ` · ${formatAgo(environment.synced_at, now)}` : ''}`
  return `not in sync${environment.error ? `: ${environment.error}` : ''}`
}
