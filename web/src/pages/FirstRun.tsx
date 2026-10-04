import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { api, type Onboarding } from '../api'
import { CheckIcon, HarnessIcon, LoopsIcon, SurfaceIcon } from '../components/Icons'
import { doneCount, nextPillar, PILLARS, surfaceTarget, type PillarKey } from '../onboarding'

// The first-run page (#581): three cards, one per pillar, each with its live
// state and a way to the place that does it. It opens the room until the
// hub has seen all three done (`Home` decides), and the nav stays live, so
// an operator who knows the way is not held here.
const ICONS: Record<PillarKey, (p: { size?: number }) => React.JSX.Element> = {
  harness: HarnessIcon,
  surface: SurfaceIcon,
  loops: LoopsIcon,
}

export default function FirstRun({ onboarding }: { onboarding: Onboarding }) {
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const next = nextPillar(onboarding)
  const done = doneCount(onboarding)
  return (
    <div className="page first-run">
      <h1>Set up your fleet</h1>
      <p className="page-lede">
        Three steps get a fleet talking. Each card checks itself, and this page steps aside once all three are
        done.
      </p>
      <div className="first-run-count" aria-live="polite">
        {done} of {PILLARS.length} done
      </div>
      <ol className="pillars">
        {PILLARS.map((p, i) => {
          const state = onboarding[p.key]
          const Icon = ICONS[p.key]
          const to = p.key === 'surface' ? surfaceTarget((loops ?? []).map((l) => l.name)) : p.to
          return (
            <li key={p.key} className={`pillar${state.done ? ' done' : ''}${p.key === next ? ' next' : ''}`}>
              <div className="pillar-head">
                <span className="pillar-icon">
                  <Icon size={28} />
                </span>
                <span className="pillar-step">{String(i + 1).padStart(2, '0')}</span>
              </div>
              <h2 className="pillar-title">{p.title}</h2>
              <p className="pillar-how">{state.done ? p.doneText : p.how}</p>
              <div className="pillar-state">
                {state.done ? (
                  <>
                    <CheckIcon size={14} /> {state.reason ?? 'done'}
                  </>
                ) : (
                  (state.reason ?? 'not done yet')
                )}
              </div>
              {to ? (
                <Link to={to} className={`btn${p.key === next ? ' primary' : ''}`}>
                  {p.action}
                </Link>
              ) : (
                <button className="btn" disabled>
                  Create a loop first
                </button>
              )}
            </li>
          )
        })}
      </ol>
    </div>
  )
}
