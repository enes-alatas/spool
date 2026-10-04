import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { api, type Onboarding } from '../api'
import { HarnessCheck } from '../components/HarnessCheck'
import { TokenDialog } from '../components/TokenDialog'
import { HarnessIcon, LoopsIcon, SurfaceIcon } from '../components/Icons'
import { doneCount, nextPillar, pillarHow, PILLARS, surfaceTarget, type PillarKey } from '../onboarding'

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
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  const [tokenOpen, setTokenOpen] = useState(false)
  // The steps already done when the page opened: their tick stands still,
  // and only a step that turns done while the operator watches draws its own.
  const [doneAtOpen] = useState(
    () => new Set(PILLARS.filter((p) => onboarding[p.key].done).map((p) => p.key)),
  )
  const bare = settings?.default_runtime === 'bare'
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
              {state.done ? (
                <DoneTick label={p.doneText} animate={!doneAtOpen.has(p.key)} />
              ) : (
                <>
                  <p className="pillar-how">{pillarHow(p, settings?.default_runtime)}</p>
                  <div className="pillar-state">{state.reason ?? 'not done yet'}</div>
                </>
              )}
              {/* The harness's actions depend on the runtime: none until the
                  settings say which, or a bare hub would offer a token. */}
              {!state.done && (p.key !== 'harness' || settings) && (
                <div className="pillar-actions">
                  {p.key === 'harness' ? (
                    // A bare hub's loops use the host's login: there is no
                    // token to give them, only the check.
                    !bare && (
                      <button
                        className={`btn${p.key === next ? ' primary' : ''}`}
                        onClick={() => setTokenOpen(true)}
                      >
                        {settings?.claude_token_set ? 'Replace token' : p.action}
                      </button>
                    )
                  ) : to ? (
                    <Link to={to} className={`btn${p.key === next ? ' primary' : ''}`}>
                      {p.action}
                    </Link>
                  ) : (
                    <button className="btn" disabled>
                      Create a loop first
                    </button>
                  )}
                  {p.key === 'harness' && (bare || settings?.claude_token_set) && (
                    <HarnessCheck pillar={state} primary={bare && p.key === next} />
                  )}
                </div>
              )}
            </li>
          )
        })}
      </ol>
      {tokenOpen && (
        <TokenDialog replacing={settings?.claude_token_set === true} onClose={() => setTokenOpen(false)} />
      )}
    </div>
  )
}

// A done step's whole body: a green tick in a ring, drawn in when the step
// turns done on screen. What it means stays with it for assistive tech and
// on hover.
function DoneTick({ label, animate }: { label: string; animate: boolean }) {
  return (
    <div className={`pillar-tick${animate ? ' animate' : ''}`} role="img" aria-label={label} title={label}>
      <svg width="56" height="56" viewBox="0 0 56 56" fill="none" aria-hidden>
        <circle className="pillar-tick-ring" cx="28" cy="28" r="26" pathLength={1} />
        <path className="pillar-tick-mark" d="M17 29l7.5 7.5L39.5 21" pathLength={1} />
      </svg>
    </div>
  )
}
