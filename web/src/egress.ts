import type { EgressView } from './api'

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
