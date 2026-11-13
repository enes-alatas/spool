import { useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import { FiguresStrip } from '../components/FiguresStrip'
import { figuresWindow } from '../figures'

// Activity is the loops' figures from the operator's Langfuse (ADR-0064):
// the strip and nothing under it. Messages are read where they are said, on
// a channel's page and a loop's page, and who changed a loop on that loop's
// page (ADR-0025's amendment of #1560).
export default function Activity() {
  // The figures' loop and window (#1301) live in the URL, so a link can
  // open the page on one loop's week. With no loop, every loop's card.
  const [params, setParams] = useSearchParams()
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  // The teams group the cards (#1563), as Fleet's query has them. A hub too
  // old for teams answers 404 and the cards are one row; until the answer
  // comes the strip waits, so the row never flashes up ahead of the groups.
  const { data: teams, isPending: teamsPending } = useQuery({
    queryKey: ['teams'],
    queryFn: api.teams,
    retry: false,
  })
  const byName = [...(loops ?? [])].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0))
  const loopNames = byName.map((l) => l.name)
  const figuresLoop = loopNames.find((n) => n === params.get('loop')) ?? ''
  const pick = (key: string, value: string, replace: boolean) =>
    setParams(
      (p) => {
        const next = new URLSearchParams(p)
        if (value) next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace },
    )

  return (
    <div className="page">
      <h1>Activity</h1>
      {loopNames.length > 0 && !teamsPending && (
        <FiguresStrip
          loops={byName}
          teams={teams}
          loop={figuresLoop}
          window={figuresWindow(params.get('window'))}
          onLoop={(name) => pick('loop', name, false)}
          onWindow={(w) => pick('window', w, true)}
        />
      )}
      {loops && loopNames.length === 0 && <div className="empty">No loops yet, so no figures to show.</div>}
    </div>
  )
}
