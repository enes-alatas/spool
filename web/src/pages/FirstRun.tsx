import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { api, type Onboarding } from '../api'
import { HarnessCheck } from '../components/HarnessCheck'
import { TokenDialog } from '../components/TokenDialog'
import { LoopDialog } from '../components/LoopDialog'
import { SurfaceDialog } from '../components/SurfaceDialog'
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

// How long the page stays once the last step's dialog closes: the tick's
// draw-in and settle, with a moment to read it, before Fleet takes over.
const STEP_ASIDE_MS = 1600

export default function FirstRun({
  onboarding,
  hold,
}: {
  onboarding: Onboarding
  hold: (held: boolean) => void
}) {
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  // Which card's dialog is open (#588, #589); one at a time.
  const [open, setOpen] = useState<PillarKey | null>(null)
  // `Home` keeps this page up while a dialog is open, so finishing the last
  // step inside one does not swap to Fleet under it.
  const openStep = (key: PillarKey) => {
    hold(true)
    setOpen(key)
  }
  const closeStep = () => {
    setOpen(null)
    if (onboarding.completed) setTimeout(() => hold(false), STEP_ASIDE_MS)
    else hold(false)
  }
  // The steps already done when the page opened: their tick stands still,
  // and only a step that turns done while the operator watches draws its own.
  const [doneAtOpen] = useState(
    () => new Set(PILLARS.filter((p) => onboarding[p.key].done).map((p) => p.key)),
  )
  const bare = settings?.default_runtime === 'bare'
  const surfaceLink = surfaceTarget((loops ?? []).map((l) => l.name))
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
          // A step that turns done under its own dialog shows done once
          // the dialog closes, so its tick draws in where it can be seen.
          const state = open === p.key ? { ...onboarding[p.key], done: false } : onboarding[p.key]
          const Icon = ICONS[p.key]
          const primary = `btn${p.key === next ? ' primary' : ''}`
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
                      <button className={primary} onClick={() => openStep('harness')}>
                        {settings?.claude_token_set ? 'Replace token' : p.action}
                      </button>
                    )
                  ) : p.key === 'loops' || (loops ?? []).length > 0 ? (
                    <button className={primary} onClick={() => openStep(p.key)}>
                      {p.action}
                    </button>
                  ) : (
                    <button className="btn" disabled>
                      Create a loop first
                    </button>
                  )}
                  {/* The loop page attaches a bot too, for an operator who
                      would rather have the whole page. */}
                  {p.key === 'surface' && surfaceLink && (
                    <Link to={surfaceLink} className="btn">
                      Open your loop
                    </Link>
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
      {open === 'harness' && (
        <TokenDialog replacing={settings?.claude_token_set === true} onClose={closeStep} />
      )}
      {open === 'loops' && <LoopDialog onClose={closeStep} />}
      {open === 'surface' && loops && <SurfaceDialog loops={loops} onClose={closeStep} />}
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
