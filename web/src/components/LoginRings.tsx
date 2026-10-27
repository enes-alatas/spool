import { useEffect, useRef, useState } from 'react'
import type { LoginPlanUsage, PlanWindow } from '../api'
import { formatAgo, formatResets, gradedColor } from '../format'
import { ringName, RING_KEY } from '../planCap'

// The plan strip on a hub with more than one Claude login (ADR-0060,
// #1074): a ring a login, its name in the middle, the 5-hour window round
// the outside and the 7-day inside, each in the graded colour the bars
// use and with its cap threshold's tick. A login no loop on it has
// reported to yet is a dashed ring. A click opens the login's figures over
// the page, without moving it; a click elsewhere or Escape closes them.
export function LoginRings({ logins, now }: { logins: LoginPlanUsage[]; now: number }) {
  const [open, setOpen] = useState<string | null>(null)
  const frame = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (open === null) return
    const away = (event: PointerEvent) => {
      if (!frame.current?.contains(event.target as Node)) setOpen(null)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(null)
    }
    document.addEventListener('pointerdown', away)
    document.addEventListener('keydown', escape)
    return () => {
      document.removeEventListener('pointerdown', away)
      document.removeEventListener('keydown', escape)
    }
  }, [open])

  return (
    <div className="login-rings" ref={frame}>
      {logins.map((login) => (
        <div className="login-ring-slot" key={login.name}>
          <button
            type="button"
            className="login-ring"
            aria-expanded={open === login.name}
            aria-label={`Plan usage of the Claude login ${login.name}`}
            title={login.unknown ?? RING_KEY}
            onClick={() => setOpen(open === login.name ? null : login.name)}
          >
            <Ring login={login} />
          </button>
          {open === login.name && <RingDetails login={login} now={now} />}
        </div>
      ))}
    </div>
  )
}

const SIZE = 80
const WIDTH = 4.5
const OUTER = SIZE / 2 - 4
const INNER = OUTER - 8

function Ring({ login }: { login: LoginPlanUsage }) {
  const half = SIZE / 2
  return (
    <svg width={SIZE} height={SIZE} viewBox={`${-half} ${-half} ${SIZE} ${SIZE}`} aria-hidden>
      {login.unknown !== undefined ? (
        <circle className="login-ring-unknown" r={OUTER} />
      ) : (
        <>
          <Arc r={OUTER} window={login.five_hour} />
          <Arc r={INNER} window={login.seven_day} />
        </>
      )}
      <text className="login-ring-name" textAnchor="middle" dominantBaseline="central">
        {ringName(login.name)}
      </text>
    </svg>
  )
}

// One window round the ring from twelve o'clock: the track, what is used,
// and the cap threshold's tick across it. A window with no figure is the
// track alone.
function Arc({ r, window }: { r: number; window: PlanWindow | null }) {
  const around = 2 * Math.PI * r
  const pct = window ? Math.round(window.used_percent) : 0
  const cap = window?.cap_percent ?? 0
  const angle = (Math.min(cap, 100) / 100) * 2 * Math.PI - Math.PI / 2
  const tick = (d: number) => [(r + d) * Math.cos(angle), (r + d) * Math.sin(angle)]
  const [x1, y1] = tick(-WIDTH / 2 - 1)
  const [x2, y2] = tick(WIDTH / 2 + 1)
  return (
    <>
      <circle className="login-ring-track" r={r} strokeWidth={WIDTH} />
      {pct > 0 && (
        <circle
          className="login-ring-used"
          r={r}
          strokeWidth={WIDTH}
          stroke={gradedColor(pct)}
          strokeDasharray={`${(Math.min(pct, 100) / 100) * around} ${around}`}
          transform="rotate(-90)"
        />
      )}
      {cap > 0 && <line className="login-ring-cap" x1={x1} y1={y1} x2={x2} y2={y2} />}
    </>
  )
}

function RingDetails({ login, now }: { login: LoginPlanUsage; now: number }) {
  return (
    <div className="login-ring-details" role="dialog" aria-label={`Plan usage of ${login.name}`}>
      <div>
        <b>{login.name}</b>
        {login.as_of !== undefined && ` · as of ${formatAgo(login.as_of, now)}`}
      </div>
      {login.unknown !== undefined ? (
        <div className="login-ring-row">{login.unknown}</div>
      ) : (
        <>
          <WindowRow name="5-hour · outer ring" window={login.five_hour} now={now} />
          <WindowRow name="7-day · inner ring" window={login.seven_day} now={now} />
        </>
      )}
    </div>
  )
}

function WindowRow({ name, window, now }: { name: string; window: PlanWindow | null; now: number }) {
  if (!window) {
    return (
      <div className="login-ring-row">
        <span>{name}</span>
        <span>unknown</span>
      </div>
    )
  }
  const pct = Math.round(window.used_percent)
  return (
    <div className="login-ring-row">
      <span>{name}</span>
      <span>
        <span style={{ color: gradedColor(pct) }}>{pct}%</span> ·{' '}
        {window.reset || !window.resets_at ? 'reset' : formatResets(window.resets_at, now)}
        {!!window.cap_percent && ` · cap ${window.cap_percent}%`}
      </span>
    </div>
  )
}
