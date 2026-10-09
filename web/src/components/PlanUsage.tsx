import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, PlanWindow } from '../api'
import { formatAgo, formatResets, formatUsd, gradedColor } from '../format'
import { capReason } from '../planCap'

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
export function PlanUsageStrip({ spentToday, costDayTitle }: { spentToday: number; costDayTitle?: string }) {
  const { data: usage, error } = useQuery({ queryKey: ['plan-usage'], queryFn: api.planUsage })
  const now = useMinuteClock()
  const unknown = error ? (error instanceof Error ? error.message : String(error)) : usage?.unknown
  const capped = capReason(usage, now)
  return (
    <>
      <div className="plan-strip-frame">
        <div className="plan-strip">
          <span className="plan-title">plan usage</span>
          {unknown !== undefined ? (
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
      {capped && <CapBanner reason={capped} />}
    </>
  )
}

// The fleet is capped (#651): why, when it wakes, and the way to lift it
// here, where the operator notices, rather than a detour to Settings.
export function CapBanner({ reason }: { reason: string }) {
  const resume = useResume()
  return (
    <div className="cap-banner" role="status">
      <span>
        <b>Fleet capped.</b> <span className="cap-reason">{reason}</span>
        {resume.error && (
          <span className="form-error" role="alert">
            {' '}
            {resume.error.message}
          </span>
        )}
      </span>
      <button className="btn" onClick={() => resume.mutate()} disabled={resume.isPending}>
        {resume.isPending ? 'Resuming…' : 'Resume now'}
      </button>
    </div>
  )
}

// Resume now: the plan read it answers with replaces the cached one, and the
// loops are read again, since every row's state changes with it.
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
