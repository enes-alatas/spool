import { doingAge, doingStale } from '../doing'
import type { LoopView } from '../api'
import { useMinuteClock } from './PlanUsage'

// A loop's doing line (#750, #752): what it said it is on, then how long
// ago. On a line of its own and never cut short (Enes's pick on #752), so
// the age always shows and the words wrap. It keeps its own minute clock,
// so only the line re-renders as its age moves on.
export function Doing({ loop, className }: { loop: LoopView; className: string }) {
  const now = useMinuteClock()
  if (!loop.doing) return null
  const age = doingAge(loop.doing_at, now)
  const stale = doingStale(loop.doing_at, now)
  return (
    <span className={`${className} doing${stale ? ' stale' : ''}`}>
      {loop.doing}
      {age && <span className="doing-age"> · {age}</span>}
    </span>
  )
}
