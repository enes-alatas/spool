import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, PlanUsage, PlanWindow } from '../api'
import { formatAgo, formatResets, formatUsd, gradedColor } from '../format'
import { capIdleStatus, cappedLogins, capReason, loopLogin, usageLogins } from '../planCap'
import { LoginRings } from './LoginRings'
import { useMay } from './Session'

// The relative times on the strip are minutes, so a render a minute keeps
// them honest without a timer per second.
export function useMinuteClock(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 60000)
    return () => clearInterval(timer)
  }, [])
  return now
}

// One limit window: its name, a bar that stretches with the strip, the
// percent and when it starts over. The bar and the percent take the graded
// colour the share column does, so the two read on one scale. A window with
// a cap threshold marks it on the bar (#651), so how close the fleet is to
// sleeping reads off the same line.
function PlanWindowStat({ name, window, now }: { name: string; window: PlanWindow | null; now: number }) {
  if (!window) {
    return (
      <span className="plan-window">
        <span>{name}</span> <span className="dim">unknown</span>
      </span>
    )
  }
  const pct = Math.round(window.used_percent)
  const color = gradedColor(pct)
  return (
    <span className="plan-window">
      <span>{name}</span>
      <span className="plan-bar" aria-hidden style={{ color }}>
        <span style={{ width: `${Math.min(pct, 100)}%` }} />
        {!!window.cap_percent && (
          <i className="plan-cap-mark" style={{ left: `${Math.min(window.cap_percent, 100)}%` }} />
        )}
      </span>
      <span className="plan-pct" style={{ color }}>
        {pct}%
      </span>{' '}
      <span>{window.reset || !window.resets_at ? 'reset' : formatResets(window.resets_at, now)}</span>
    </span>
  )
}

// What the fleet is costing, in one line between the header and the tabs:
// the plan's 5-hour and 7-day usage (#648) and the fleet's spend today, which
// moved here from the header so it stands over the TODAY column it totals.
// The spend is the rows' own sum and shows whatever the plan read says; a
// hub that cannot read the plan says "unknown", with its reason on hover.
// A hub with more than one Claude login (ADR-0060) shows a ring a login in
// the bars' place (LoginRings); a hub with one keeps the bars.
export function PlanUsageStrip({ spentToday, costDayTitle }: { spentToday: number; costDayTitle?: string }) {
  const { data: usage, error } = useQuery({ queryKey: ['plan-usage'], queryFn: api.planUsage })
  const now = useMinuteClock()
  const unknown = error ? (error instanceof Error ? error.message : String(error)) : usage?.unknown
  const logins = error ? undefined : usageLogins(usage)
  const sleeps = useSleeps(usage)
  return (
    <>
      <div className="plan-strip-frame">
        <div className="plan-strip">
          <span className="plan-title">plan usage</span>
          {logins ? (
            <LoginRings
              logins={logins}
              now={now}
              sleeps={sleeps}
              resume={(login) => <ResumeButton login={login} />}
            />
          ) : unknown !== undefined ? (
            <span className="plan-window dim" title={unknown}>
              unknown
            </span>
          ) : (
            usage && (
              <>
                <PlanWindowStat name="5-hour" window={usage.five_hour} now={now} />
                <PlanWindowStat name="7-day" window={usage.seven_day} now={now} />
                {usage.as_of !== undefined && (
                  <span className="plan-as-of" title={new Date(usage.as_of).toLocaleString()}>
                    as of {formatAgo(usage.as_of, now)}
                  </span>
                )}
              </>
            )
          )}
          <span className="plan-spend" title={costDayTitle}>
            {formatUsd(spentToday)} <span className="lbl-inline">today</span>
          </span>
        </div>
      </div>
      {/* With several logins a capped one shows on its ring, which says
          what the banner would (Enes's call on #1076). */}
      {!logins && <CapBanners usage={usage} now={now} />}
    </>
  )
}

// A banner for each Claude login whose plan cap holds (#1075): on a hub
// with one login the fleet's, as it always was. With idle, the logins not
// capped say where their usage stands instead, as the Settings section
// does over its thresholds.
export function CapBanners({
  usage,
  now,
  idle,
}: {
  usage: PlanUsage | undefined
  now: number
  idle?: boolean
}) {
  const several = !!usageLogins(usage)
  const capped = cappedLogins(usage)
  const sleeps = useSleeps(usage)
  const idleLines = !idle
    ? []
    : several
      ? usageLogins(usage)!
          .filter((login) => !login.cap)
          .map((login) => `${login.name} · ${capIdleStatus(login, now)}`)
      : capped.length === 0
        ? [capIdleStatus(usage, now)].filter((line) => line !== undefined)
        : []
  return (
    <>
      {capped.map((login) => (
        <CapBanner
          key={login.name}
          login={several ? login.name : undefined}
          reason={capReason(login, now, sleeps(login.name))!}
        />
      ))}
      {idleLines.length > 0 && (
        <div className="cap-idle">
          {idleLines.map((line) => (
            <div key={line}>{line}</div>
          ))}
        </div>
      )}
    </>
  )
}

// The active loops on a login, which its cap sleeps, by the login's name;
// undefined on a hub with one login, where the cap sleeps every loop, and
// until the loops have loaded, so the copy says "Loops sleep" rather than
// that none runs on it.
function useSleeps(usage: PlanUsage | undefined): (login: string) => string[] | undefined {
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const several = !!usageLogins(usage)
  return (login) =>
    several && loops
      ? loops.filter((loop) => loop.status === 'active' && loopLogin(loop) === login).map((loop) => loop.name)
      : undefined
}

// A login is capped (#651, #1075): why, which loops sleep and when they
// wake, and the way to lift it here, where the operator notices, rather
// than a detour to Settings. Without a login it is the fleet's, on a hub
// with one login.
function CapBanner({ login, reason }: { login?: string; reason: string }) {
  const resume = useResume()
  // Lifting the cap is an admin's (#679); a member is told why it holds.
  const manage = useMay()('manage')
  return (
    <div className="cap-banner" role="status">
      <span>
        <b>{login === undefined ? 'Fleet capped.' : `${login} capped.`}</b>{' '}
        <span className="cap-reason">{reason}</span>
        {resume.error && (
          <span className="form-error" role="alert">
            {' '}
            {resume.error.message}
          </span>
        )}
      </span>
      {manage && (
        <button
          className="btn"
          aria-label={login === undefined ? undefined : `Resume now: lift ${login}'s plan cap`}
          onClick={() => resume.mutate(login)}
          disabled={resume.isPending}
        >
          {resume.isPending ? 'Resuming…' : 'Resume now'}
        </button>
      )}
    </div>
  )
}

// Resume now inside a capped ring's details, for those who may lift the
// cap, with its error under the details' figures.
function ResumeButton({ login }: { login: string }) {
  const resume = useResume()
  if (!useMay()('manage')) return null
  return (
    <span>
      <button
        className="btn"
        aria-label={`Resume now: lift ${login}'s plan cap`}
        onClick={() => resume.mutate(login)}
        disabled={resume.isPending}
      >
        {resume.isPending ? 'Resuming…' : 'Resume now'}
      </button>
      {resume.error && (
        <span className="form-error" role="alert">
          {' '}
          {resume.error.message}
        </span>
      )}
    </span>
  )
}

// Resume now, for a login or the default: the plan read it answers with
// replaces the cached one, and the loops are read again, since the rows on
// that login change state with it.
function useResume() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.resumePlanCap,
    onSuccess: (usage) => {
      qc.setQueryData(['plan-usage'], usage)
      void qc.invalidateQueries({ queryKey: ['loops'] })
    },
  })
}
