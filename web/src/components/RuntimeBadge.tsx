import type { LoopView } from '../api'
import { CLOUD_BADGE_TITLE } from '../options'

// What a loop's runtime says beside its name: *uncontained* for a bare loop,
// in the alarm colour, since it runs with the operator's own files and
// network (ADR-0017), and a neutral *cloud* for a cloud loop (ADR-0056, the
// operator's pick on #867), whose caveat is its tooltip. A container says
// nothing: it is what a loop is unless told otherwise.
export function RuntimeBadge({ runtime }: { runtime: LoopView['runtime'] }) {
  if (runtime === 'bare') return <span className="containment-badge">uncontained</span>
  if (runtime === 'cloud')
    return (
      <span className="containment-badge cloud" title={CLOUD_BADGE_TITLE}>
        cloud
      </span>
    )
  return null
}
