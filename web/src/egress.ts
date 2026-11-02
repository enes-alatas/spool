import type { EgressGroup, EgressView } from './api'

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

// The operator's hosts under the Egress dialog's filter (#971): those whose
// name contains it, in any case. An empty filter, or one of spaces, keeps
// them all.
export function filterHosts(hosts: string[], filter: string): string[] {
  const query = filter.trim().toLowerCase()
  return hosts.filter((host) => host.toLowerCase().includes(query))
}

export function countHosts(groups: EgressGroup[]): number {
  return groups.reduce((n, g) => n + g.hosts.length, 0)
}

// Whether the fleet's allowlist already lets a workstation reach host on
// 443, as the hub's egress.Allowlist reads it: an entry naming the host with
// no port or with :443, or a domain entry (".example.com") covering it.
export function egressAllows(view: EgressView, host: string): boolean {
  const name = host.trim().toLowerCase().replace(/\.$/, '')
  return [...view.built_in.flatMap((g) => g.hosts), ...view.extra].some((raw) => {
    const entry = raw.trim().toLowerCase()
    const colon = entry.lastIndexOf(':')
    const port = colon > 0 ? entry.slice(colon + 1) : ''
    if (port && port !== '443') return false
    const at = (port ? entry.slice(0, colon) : entry).replace(/\.$/, '')
    if (at.startsWith('.')) return name.endsWith(at) || name === at.slice(1)
    return name === at
  })
}
