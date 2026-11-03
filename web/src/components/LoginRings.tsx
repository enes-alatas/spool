import { type ReactNode, useEffect, useRef, useState } from 'react'
import type { LoginPlanUsage, PlanWindow } from '../api'
import { formatAgo, formatResets, gradedColor } from '../format'
import {
  capWake,
  CLOUD_ONLY_FROZEN,
  CLOUD_ONLY_FROZEN_WHY,
  CLOUD_ONLY_USAGE,
  CLOUD_ONLY_WHY,
  cloudReading,
  type CloudReading,
  ringName,
  RING_KEY,
} from '../planCap'

// The plan strip on a hub with more than one Claude login (ADR-0060,
// #1074): a ring a login, its name in the middle, the 5-hour window round
// the outside and the 7-day inside, each in the graded colour the bars
// use and with its cap threshold's tick. A login no loop on it has
// reported to yet is a dashed ring. A click opens the login's figures over
// the page, without moving it; a click elsewhere or Escape closes them.
//
// A login whose plan cap holds (#1076) wears a red rim and says "capped"
// under its name, and its details name the loops that sleep (sleeps) and
// when they wake, with resume (Resume now, for those who may) beside it.
// The Fleet page has no cap banner then: the ring says it.
//
// A login whose loops all run in the cloud (cloudOnly, #1303) gets no new
// reading. While its last one still counts, the cap acts on it, so the ring
// keeps it and the details say it won't update; once nothing live is left
// the ring is dashed and the details say the cap does not act.
export function LoginRings({
  logins,
  cloudOnly,
  now,
  sleeps,
  resume,
}: {
  logins: LoginPlanUsage[]
  cloudOnly: Set<string>
  now: number
  sleeps: (login: string) => string[] | undefined
  resume?: (login: string) => ReactNode
}) {
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
      {logins.map((login) => {
        const cloud = cloudReading(login, cloudOnly.has(login.name))
        return (
          <div className="login-ring-slot" key={login.name}>
            <button
              type="button"
              className="login-ring"
              aria-expanded={open === login.name}
              aria-label={`Plan usage of the Claude login ${login.name}`}
              title={
                cloud === 'unknown'
                  ? CLOUD_ONLY_WHY
                  : cloud === 'last'
                    ? CLOUD_ONLY_FROZEN_WHY
                    : (login.unknown ?? RING_KEY)
              }
              onClick={() => setOpen(open === login.name ? null : login.name)}
            >
              <Ring login={login} unknown={cloud === 'unknown' || login.unknown !== undefined} />
            </button>
            {open === login.name && (
              <RingDetails
                login={login}
                cloud={cloud}
                now={now}
                sleeps={sleeps(login.name)}
                resume={resume}
              />
            )}
          </div>
        )
      })}
    </div>
  )
}

const SIZE = 80
const WIDTH = 4.5
const OUTER = SIZE / 2 - 4
const INNER = OUTER - 8

function Ring({ login, unknown }: { login: LoginPlanUsage; unknown: boolean }) {
  const half = SIZE / 2
  return (
    <svg
      width={SIZE}
      height={SIZE}
      viewBox={`${-half} ${-half} ${SIZE} ${SIZE}`}
      overflow="visible"
      aria-hidden
    >
      {unknown ? (
        <circle className="login-ring-unknown" r={OUTER} />
      ) : (
        <>
          <Arc r={OUTER} window={login.five_hour} />
          <Arc r={INNER} window={login.seven_day} />
        </>
      )}
      {login.cap && <circle className="login-ring-capped" r={OUTER + WIDTH / 2 + 2.5} />}
      <text className="login-ring-name" textAnchor="middle" dominantBaseline="central" y={login.cap ? -5 : 0}>
        {ringName(login.name)}
      </text>
      {login.cap && (
        <text className="login-ring-capped-label" textAnchor="middle" dominantBaseline="central" y={8}>
          capped
        </text>
      )}
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

function RingDetails({
  login,
  cloud,
  now,
  sleeps,
  resume,
}: {
  login: LoginPlanUsage
  cloud: CloudReading
  now: number
  sleeps?: string[]
  resume?: (login: string) => ReactNode
}) {
  return (
    <div className="login-ring-details" role="dialog" aria-label={`Plan usage of ${login.name}`}>
      <div>
        <b>{login.name}</b>
        {cloud !== 'unknown' && login.as_of !== undefined && ` · as of ${formatAgo(login.as_of, now)}`}
      </div>
      {cloud === 'unknown' ? (
        <>
          <div className="login-ring-row">{CLOUD_ONLY_USAGE}</div>
          <div className="login-ring-row login-ring-why">{CLOUD_ONLY_WHY}</div>
        </>
      ) : login.unknown !== undefined ? (
        <div className="login-ring-row">{login.unknown}</div>
      ) : (
        <>
          <WindowRow name="5-hour · outer ring" window={login.five_hour} now={now} />
          <WindowRow name="7-day · inner ring" window={login.seven_day} now={now} />
          {cloud === 'last' && (
            <>
              <div className="login-ring-row">{CLOUD_ONLY_FROZEN}</div>
              <div className="login-ring-row login-ring-why">{CLOUD_ONLY_FROZEN_WHY}</div>
            </>
          )}
        </>
      )}
      {login.cap && (
        <div className="login-ring-row login-ring-capped-row">
          <span className="login-ring-capped-note">Capped. {capWake(login.cap, now, sleeps)}</span>
          {resume?.(login.name)}
        </div>
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
