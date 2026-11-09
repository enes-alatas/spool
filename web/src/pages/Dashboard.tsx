import { RuntimeBadge } from '../components/RuntimeBadge'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { api, LoopView, Settings } from '../api'
import {
  formatTokens,
  fillTone,
  hasFillPct,
  formatSpend,
  formatUsd,
  inTurn,
  nextWake,
  gradedColor,
  spendShare,
  sumCostToday,
} from '../format'
import { StateDot } from '../components/Spool'
import { PlanUsageStrip } from '../components/PlanUsage'
import { Doing } from '../components/Doing'
import { HealthNote } from '../components/Health'
import { useMay } from '../components/Session'
import { FleetTeams, OrbKey } from '../components/FleetTeams'
import { FleetColumns } from '../components/FleetColumns'
import { undeliveredNote } from '../messages'
import { workstationNote } from '../workstation'
import { unnamedCloudHold, useCloudSettings } from '../cloudLoop'
import { useEffect, useState } from 'react'
import { cappedBy } from '../dailyCap'
import { loopPlanCapped } from '../planCap'
import { channelChatPath, FLEET_CHANNEL } from '../channels'
import { readFleetLayout, saveFleetLayout, type FleetLayout } from '../teams'

function Countdown({ at }: { at: number }) {
  const [s, setS] = useState<number | null>(null)
  useEffect(() => {
    const update = () => setS(Math.max(0, Math.floor((at - Date.now()) / 1000)))
    update()
    const t = setInterval(update, 1000)
    return () => clearInterval(t)
  }, [at])
  if (!at || s === null) return <span className="dim">·</span>
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
  if (!loop.context_tokens) return <span className="dim">·</span>
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

// The row's part of the fleet's day, as a percent and a bar that draws it, so
// the loop eating the plan stands out down the column without reading every
// figure. On a day the fleet has spent nothing every row reads 0% over an
// empty bar.
function ShareStat({ cost, total }: { cost: number; total: number }) {
  const pct = spendShare(cost, total)
  // The label outside the coloured span, as the context gauge keeps "ctx": the
  // colour is the figure's, not the words'.
  return (
    <>
      <span
        className="share"
        style={{ color: gradedColor(pct) }}
        title={`${formatUsd(cost)} of the fleet's ${formatUsd(total)} today`}
      >
        <span className="share-bar" aria-hidden>
          <span style={{ width: `${pct}%` }} />
        </span>
        {pct}%
      </span>{' '}
      <span className="lbl after">of fleet</span>
    </>
  )
}

// One row per loop: identity on the left, the operational numbers to the
// right of it. The metadata is wrapped in an element that is `display:
// contents` on a wide screen — its children join the row's grid as columns —
// and a wrapping line of its own once the columns no longer fit.
function FleetRow({
  loop,
  thresholds,
  spentToday,
  planCapped,
}: {
  loop: LoopView
  thresholds?: Settings
  spentToday: number
  // Its login's plan cap has fired (#651, #1075): a loop still in its turn
  // sleeps when it ends, so its next wake is the cap's, not one it will
  // choose.
  planCapped: boolean
}) {
  const nav = useNavigate()
  const hold = unnamedCloudHold(loop, useCloudSettings(loop))
  // An unconfirmed allowlist is a warning, never a hold (ADR-0059),
  // so a hold or a session down outranks it.
  const station: { text: string; bad: boolean; warn?: boolean } | undefined = hold
    ? { text: `needs setup: ${hold}`, bad: true }
    : (workstationNote(loop) ??
      (loop.cloud_allowlist_unconfirmed
        ? { text: 'allowlist not confirmed', bad: false, warn: true }
        : undefined))
  const undelivered = undeliveredNote(loop.undelivered)
  // Its own daily cap takes a loop at its turn's end the same way (#652).
  const cappedNext = planCapped || !!loop.daily_cap_reached
  return (
    <Link to={`/loops/${loop.name}`} className="fleet-row">
      <div className="identity">
        <span className="loop-name">{loop.name}</span>
        {/* One row, so every loop's row is the same height whatever its
            mission says; the hover and the loop's page have the whole of it
            (#752). */}
        <span className="mission" title={loop.mission}>
          {loop.mission}
        </span>
        {(loop.runtime === 'bare' ||
          loop.runtime === 'cloud' ||
          station ||
          undelivered ||
          loop.branch ||
          loop.tg_bot_username ||
          loop.doing ||
          loop.problems.length > 0) && (
          <span className="notes">
            {/* First: a loop the health check has a problem with is the
                row's news (ADR-0053). */}
            <HealthNote loop={loop} />
            <RuntimeBadge runtime={loop.runtime} />
            {station && (
              <span className={station.bad ? 'note bad' : station.warn ? 'note warn' : 'note'}>
                {station.text}
              </span>
            )}
            {/* With the faults rather than with the metadata: a loop whose
                messages are not arriving is something to act on, and the
                branch and bot handle beside it are not. */}
            {/* Opens this loop's Undelivered pane rather than the row's
                default one (#281). Not a `Link`: the row already is one, and
                a link inside a link is invalid, so it steers the row's
                navigation instead of adding its own. */}
            {undelivered && (
              <span
                className="note bad undelivered-link"
                title={undelivered.title}
                onClick={(e) => {
                  e.preventDefault()
                  const pane = `/loops/${loop.name}?pane=undelivered`
                  // A modified click means a new tab, as it does on the rest
                  // of the row — on the pane, which is what was clicked.
                  if (e.metaKey || e.ctrlKey) window.open(pane, '_blank', 'noopener')
                  else nav(pane)
                }}
              >
                {undelivered.text}
              </span>
            )}
            {loop.workspace_mode === 'worktree' && loop.branch && <span className="note">{loop.branch}</span>}
            {loop.tg_bot_username && <span className="note">@{loop.tg_bot_username}</span>}
            <Doing loop={loop} className="note" />
          </span>
        )}
      </div>
      <span
        className={`f-state${loop.state === 'capped' ? ' capped' : ''}`}
        title={loop.state === 'capped' ? cappedBy(loop, planCapped) : undefined}
      >
        <StateDot state={loop.state} />
        {loop.state}
      </span>
      <div className="f-meta">
        <span className="f-next">
          {/* "next turn in progress" would be one word too many: the words
              already say what the label would. */}
          {!inTurn(loop.state) && (
            <>
              <span className="lbl">next</span>{' '}
            </>
          )}
          {inTurn(loop.state) ? (
            // The approved designs say this differently at the two widths: a
            // dash under the NEXT WAKE column header, words where there is no
            // header to explain the dash.
            <>
              <span className="only-wide dim">{cappedNext ? 'capped next' : '·'}</span>
              <span className="only-narrow hot">turn in progress{cappedNext && ', then capped'}</span>
            </>
          ) : (
            <Countdown at={nextWake(loop)} />
          )}
        </span>
        <span className="f-ctx">
          <ContextStat loop={loop} thresholds={thresholds} /> <span className="lbl after">ctx</span>
        </span>
        <span className="f-share">
          <ShareStat cost={loop.cost_today_usd} total={spentToday} />
        </span>
        <span className="f-today" title={formatSpend(loop).title}>
          {formatSpend(loop).text} <span className="lbl after">today</span>
        </span>
      </div>
    </Link>
  )
}

// Which day "today" is. The server decides the boundary — its own local
// midnight, not UTC's (#186) — and says so, so the room names the day rather
// than deriving one the server might not agree with. Hover text, not a column:
// an operator watching one fleet knows what day it is, and only wants to check
// when a number looks wrong.
function costDayTitle(loops: LoopView[]): string | undefined {
  const day = loops[0]?.cost_day
  return day ? `spend on ${day}, the server's local day` : undefined
}

export default function Dashboard() {
  const may = useMay()
  const { data: loops, isLoading, isError, error } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  // the rotation thresholds the gauge colours mean something against
  const { data: thresholds } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  // the strip's own read, shared through the cache: whether each login's
  // cap has fired
  const { data: usages } = useQuery({ queryKey: ['plan-usage'], queryFn: api.planUsage })

  // The fleet as its teams (#1411). A hub too old for teams answers 404, and
  // the fleet shows as rows as it did before. Until the answer comes, neither
  // shows, so the rows never flash up ahead of the cards.
  const { data: teams, isPending: teamsPending } = useQuery({
    queryKey: ['teams'],
    queryFn: api.teams,
    retry: false,
  })
  // Summed from the rows already on screen: the list endpoint carries each
  // loop's daily cost, so the fleet's bill for the day costs no request of its
  // own. It stands on the plan strip, over the TODAY column it totals, because
  // it answers the question the plan's usage does — what is this fleet costing
  // — and it is the one number in the room the operator was previously running
  // SQL for.
  const spentToday = sumCostToday(loops ?? [])
  // The fleet channel's chat was a tab here (#286) until the page became the
  // fleet and nothing else (#1443); a saved link to the tab lands on the chat
  // on the Channels page.
  const [params] = useSearchParams()
  const oldChannelTab = params.get('view') === 'channel'
  // Teams or the rows they replaced (#1411): the cards are new, so the
  // rows stay a click away for an operator who reads the fleet that way.
  // Remembered in this browser, as how one person likes to read the page.
  const [layout, setLayout] = useState<FleetLayout>(readFleetLayout)
  const chooseLayout = (next: FleetLayout) => {
    setLayout(next)
    saveFleetLayout(next)
  }

  if (oldChannelTab) return <Navigate to={channelChatPath(FLEET_CHANNEL)} replace />

  return (
    <div className="page">
      {teams ? (
        // The nav already names the page, and a title over the cards
        // outweighed them; the counts sit on the cards' legend row instead.
        <h1 className="sr-only">Fleet</h1>
      ) : (
        <div className="fleet-head">
          <h1>Fleet</h1>
          {loops && loops.length > 0 && (
            <span className="fleet-summary">
              {loops.length} {loops.length === 1 ? 'loop' : 'loops'}
            </span>
          )}
        </div>
      )}
      {loops && loops.length > 0 && (
        <PlanUsageStrip spentToday={spentToday} costDayTitle={costDayTitle(loops)} />
      )}

      <>
        {(isLoading || (loops && loops.length > 0 && teamsPending)) && (
          <div className="fleet-note">Loading the fleet…</div>
        )}
        {isError && (
          <div className="form-error" role="alert">
            Could not load the fleet: {error instanceof Error ? error.message : String(error)}
          </div>
        )}

        {loops && loops.length === 0 && (
          <div className="empty">
            {may('manage') ? (
              <>
                No loops yet. Create one and give it a mission.
                <div style={{ marginTop: 14 }}>
                  <Link to="/new" className="btn primary">
                    New loop
                  </Link>
                </div>
              </>
            ) : (
              'No loops yet. An admin creates them.'
            )}
          </div>
        )}

        {loops && loops.length > 0 && teams && (
          <div className="fleet-bar">
            {layout === 'teams' && <OrbKey />}
            {/* Counts only: what needs the operator is on the cards. */}
            <span className="fleet-summary">
              {teams.length} {teams.length === 1 ? 'team' : 'teams'} · {loops.length}{' '}
              {loops.length === 1 ? 'loop' : 'loops'}
            </span>
            <div className="figures-windows" role="group" aria-label="Fleet layout">
              {(['teams', 'list'] as const).map((option) => (
                <button
                  key={option}
                  type="button"
                  className={`figures-window${option === layout ? ' on' : ''}`}
                  aria-pressed={option === layout}
                  onClick={() => chooseLayout(option)}
                >
                  {option === 'teams' ? 'Teams' : 'List'}
                </button>
              ))}
            </div>
          </div>
        )}
        {loops && loops.length > 0 && teams && layout === 'teams' && (
          <FleetTeams teams={teams} loops={loops} spentToday={spentToday} manage={may('manage')} />
        )}
        {loops && loops.length > 0 && ((!teams && !teamsPending) || (teams && layout === 'list')) && (
          <div className="fleet-list">
            <FleetColumns />
            {loops.map((fleetLoop) => (
              <FleetRow
                key={fleetLoop.id}
                loop={fleetLoop}
                thresholds={thresholds}
                spentToday={spentToday}
                planCapped={loopPlanCapped(usages, fleetLoop)}
              />
            ))}
          </div>
        )}
      </>
    </div>
  )
}
