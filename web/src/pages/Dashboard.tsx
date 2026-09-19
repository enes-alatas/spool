import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, LoopView, Settings } from '../api'
import { formatTokens, fillTone, hasFillPct, formatUsd, sumCostToday } from '../format'
import { StateDot } from '../components/Spool'
import { useEffect, useState } from 'react'

function Countdown({ at }: { at: number }) {
  const [s, setS] = useState<number | null>(null)
  useEffect(() => {
    const update = () => setS(Math.max(0, Math.floor((at - Date.now()) / 1000)))
    update()
    const t = setInterval(update, 1000)
    return () => clearInterval(t)
  }, [at])
  if (!at || s === null) return <span>—</span>
  if (s === 0) return <span className="hot">due</span>
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (h > 0) return <span>{`${h}h ${m}m`}</span>
  if (m > 0) return <span>{`${m}m ${s % 60}s`}</span>
  return <span className="hot">{`${s}s`}</span>
}

// Context occupancy in one number: the percentage of the model's window when
// Spool knows it, and the raw token count when it doesn't — never a
// percentage against a guessed limit.
function ContextStat({ loop, thresholds }: { loop: LoopView; thresholds?: Settings }) {
  // Falsy, not `=== 0`: api.ts describes the API as it will be, so a field an
  // older server doesn't send arrives as undefined and would divide into NaN.
  if (!loop.context_tokens) return <span className="dim">—</span>
  // The limit, not the percentage, answers "does Spool know the window": the
  // percentage truncates, so a measured loop holding ~1,500 tokens of a 200k
  // window reports 0%, which is a true reading and not an absent one. The loop
  // page asks the same question of the same field.
  // Knowing the window is not the same as having been told the fill: an older
  // server sends the limit and not the percentage, and the tokens are the
  // honest reading of that.
  if (!loop.context_limit_tokens || !hasFillPct(loop.context_fill_pct))
    return <span>{formatTokens(loop.context_tokens)}</span>
  return (
    <span
      className={fillTone(loop.context_fill_pct, thresholds)}
      title={`${loop.context_tokens} of ${loop.context_limit_tokens} context tokens`}
    >
      {loop.context_fill_pct}%
    </span>
  )
}

// A workstation the operator switched off is not a fault; only an unreachable
// one is worth flagging. Both are said in the row rather than in hover text —
// a warning nobody can see on a touch screen is not a warning.
function workstationNote(loop: LoopView): { text: string; bad: boolean } | undefined {
  if (loop.workstation_up) return undefined
  if (loop.down_reason === 'powered_off') return { text: 'workstation off', bad: false }
  return { text: `workstation down: ${loop.workstation_detail || 'unreachable'}`, bad: true }
}

// One row per loop: identity on the left, the four operational numbers to the
// right of it. The metadata is wrapped in an element that is `display:
// contents` on a wide screen — its children join the row's grid as columns —
// and a wrapping line of its own once the columns no longer fit.
function FleetRow({ loop, thresholds }: { loop: LoopView; thresholds?: Settings }) {
  const station = workstationNote(loop)
  return (
    <Link to={`/loops/${loop.name}`} className="fleet-row">
      <div className="identity">
        <span className="loop-name">{loop.name}</span>
        <span className="mission">{loop.mission}</span>
        {(loop.runtime === 'bare' || station || loop.branch || loop.tg_bot_username) && (
          <span className="notes">
            {loop.runtime === 'bare' && <span className="containment-badge">uncontained</span>}
            {station && <span className={station.bad ? 'note bad' : 'note'}>{station.text}</span>}
            {loop.workspace_mode === 'worktree' && loop.branch && <span className="note">{loop.branch}</span>}
            {loop.tg_bot_username && <span className="note">@{loop.tg_bot_username}</span>}
          </span>
        )}
      </div>
      <span className="f-state">
        <StateDot state={loop.state} />
        {loop.state}
      </span>
      <div className="f-meta">
        <span className="f-next">
          {/* "next turn in progress" would be one word too many: the words
              already say what the label would. */}
          {loop.state !== 'busy' && (
            <>
              <span className="lbl">next</span>{' '}
            </>
          )}
          {loop.state === 'busy' ? (
            // The approved designs say this differently at the two widths: a
            // dash under the NEXT WAKE column header, words where there is no
            // header to explain the dash.
            <>
              <span className="only-wide dim">—</span>
              <span className="only-narrow hot">turn in progress</span>
            </>
          ) : (
            <Countdown at={loop.next_tick_at} />
          )}
        </span>
        <span className="f-ctx">
          <ContextStat loop={loop} thresholds={thresholds} /> <span className="lbl after">ctx</span>
        </span>
        <span className="f-today">
          {formatUsd(loop.cost_today_usd)} <span className="lbl after">today</span>
        </span>
      </div>
    </Link>
  )
}

export default function Dashboard() {
  const { data: loops, isLoading, isError, error } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  // the rotation thresholds the gauge colours mean something against
  const { data: thresholds } = useQuery({ queryKey: ['settings'], queryFn: api.settings })

  const busy = (loops ?? []).filter((fleetLoop) => fleetLoop.state === 'busy').length
  // Summed from the rows already on screen: the list endpoint carries each
  // loop's daily cost, so the fleet's bill for the day costs no request of its
  // own. It sits with the counts because it answers the same question they do
  // — what is this fleet doing right now — and it is the one number in the
  // room the operator was previously running SQL for.
  const spentToday = sumCostToday(loops ?? [])

  return (
    <div className="page">
      <div className="fleet-head">
        <h1>Fleet</h1>
        {loops && loops.length > 0 && (
          <span className="fleet-summary">
            {loops.length} {loops.length === 1 ? 'loop' : 'loops'}
            {busy > 0 && ` · ${busy} busy`}
            {` · ${formatUsd(spentToday)} today`}
          </span>
        )}
      </div>

      {isLoading && <div className="fleet-note">Loading the fleet…</div>}
      {isError && (
        <div className="form-error">
          Could not load the fleet: {error instanceof Error ? error.message : String(error)}
        </div>
      )}

      {loops && loops.length === 0 && (
        <div className="empty">
          No loops yet. Create one and give it a mission.
          <div style={{ marginTop: 14 }}>
            <Link to="/new" className="btn primary">
              New loop
            </Link>
          </div>
        </div>
      )}

      {loops && loops.length > 0 && (
        <div className="fleet-list">
          <div className="fleet-cols" aria-hidden>
            <span>loop</span>
            <span className="f-state">state</span>
            <div className="f-meta">
              <span className="f-next">next wake</span>
              <span className="f-ctx">context</span>
              <span className="f-today">today</span>
            </div>
          </div>
          {loops.map((fleetLoop) => (
            <FleetRow key={fleetLoop.id} loop={fleetLoop} thresholds={thresholds} />
          ))}
        </div>
      )}
    </div>
  )
}
