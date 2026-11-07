import { useState, type ReactNode } from 'react'
import { keepPreviousData, useQueries, useQuery } from '@tanstack/react-query'
import { api, type FiguresTurn, type FiguresWindow, type LoopFigures } from '../api'
import {
  FIGURES_WINDOWS,
  LANGFUSE_DOCS,
  allOff,
  cardLine,
  count,
  figuresRefetch,
  turnAt,
  figuresAge,
  figuresKey,
  figuresLine,
  formatDuration,
  perTurn,
  toolMix,
  turnShares,
} from '../figures'
import { formatAgo, formatTokens, formatUsd } from '../format'
import { messageTime } from './MessageKnot'
import { useMinuteClock } from './PlanUsage'

// How the loops are doing (#1301), from the figures the hub reads back from
// Langfuse (#1300). A window to pick, then a card a loop by default, each
// with its spend, turns and turn duration over its turns' bars; a card
// opens that loop's detail: its tokens, each turn's spend, and the tools
// and failures. Numbers and names only: no trace content exists to show.
// Data is drawn in the text colour, and red marks only what failed
// (ADR-0027). A hub without the route answers 404, and the strip then
// shows nothing rather than an error, as it isn't the page's point.
export function FiguresStrip({
  loops,
  loop,
  window,
  onLoop,
  onWindow,
}: {
  loops: string[]
  // '' for the overview
  loop: string
  window: FiguresWindow
  onLoop: (name: string) => void
  onWindow: (w: FiguresWindow) => void
}) {
  const windows = (
    <div className="figures-windows" role="group" aria-label="Window">
      {FIGURES_WINDOWS.map((w) => (
        <button
          key={w}
          type="button"
          className={`figures-window${w === window ? ' on' : ''}`}
          aria-pressed={w === window}
          onClick={() => onWindow(w)}
        >
          {w}
        </button>
      ))}
    </div>
  )
  return loop === '' ? (
    <Overview loops={loops} window={window} windows={windows} onLoop={onLoop} />
  ) : (
    <Detail loops={loops} loop={loop} window={window} windows={windows} onLoop={onLoop} />
  )
}

function figuresQuery(loop: string, window: FiguresWindow) {
  return {
    queryKey: figuresKey(loop, window),
    queryFn: () => api.figures(loop, window),
    retry: false,
    refetchInterval: (q: { state: { data?: LoopFigures } }) => figuresRefetch(q.state.data),
    // another window keeps the last answer up until its own arrives
    placeholderData: keepPreviousData,
  }
}

function Overview({
  loops,
  window,
  windows,
  onLoop,
}: {
  loops: string[]
  window: FiguresWindow
  windows: ReactNode
  onLoop: (name: string) => void
}) {
  const answers = useQueries({ queries: loops.map((name) => figuresQuery(name, window)) })
  const served = answers.flatMap((a) => (a.data ? [a.data] : []))
  if (served.length === 0) return null
  const off = allOff(served)
  return (
    <section className="figures" aria-label="Figures">
      <div className="figures-head">
        <span className="figures-title">Figures</span>
        {windows}
      </div>
      {off ? (
        <Body figures={served[0]} />
      ) : (
        <div className="figures-cards">
          {served.map((f) => (
            <Card key={f.loop} figures={f} onOpen={() => onLoop(f.loop)} />
          ))}
        </div>
      )}
    </section>
  )
}

// A loop's card: its spend, turns and median turn over its turns' bars,
// or a short line why there are none. It opens the loop's detail.
function Card({ figures, onOpen }: { figures: LoopFigures; onOpen: () => void }) {
  const now = useMinuteClock()
  const line = cardLine(figures)
  const f = figures.figures
  const age = figuresAge(figures, now)
  return (
    <button type="button" className="figures-card" onClick={onOpen} title={`Open @${figures.loop}'s figures`}>
      <span className="figures-card-head">
        <span className="figures-card-name">@{figures.loop}</span>
        {age?.stale && <span className="figures-age stale">{formatAgo(figures.fetched_at, now)}</span>}
      </span>
      {line || !f ? (
        <span className="figures-card-line">{line}</span>
      ) : (
        <>
          <span className="figures-value">{formatUsd(f.cost_usd)}</span>
          <span className="figures-sub">
            {count(f.turns, 'turn')}
            {f.failed_turns > 0 && <span className="figures-failed"> · {f.failed_turns} failed</span>} ·{' '}
            {formatDuration(f.turn_duration_ms.p50)} median
          </span>
          {f.per_turn.length > 0 && <Bars turns={f.per_turn} />}
        </>
      )}
    </button>
  )
}

function Detail({
  loops,
  loop,
  window,
  windows,
  onLoop,
}: {
  loops: string[]
  loop: string
  window: FiguresWindow
  windows: ReactNode
  onLoop: (name: string) => void
}) {
  const { data } = useQuery(figuresQuery(loop, window))
  if (!data) return null
  return (
    <section className="figures" aria-label="Figures">
      <div className="figures-head">
        <button type="button" className="text-button figures-back" onClick={() => onLoop('')}>
          ← All loops
        </button>
        <select
          className="panel-select"
          aria-label="Loop"
          value={loop}
          onChange={(e) => onLoop(e.target.value)}
        >
          {loops.map((name) => (
            <option key={name} value={name}>
              @{name}
            </option>
          ))}
        </select>
        {windows}
        <Age figures={data} />
      </div>
      <Body figures={data} />
    </section>
  )
}

function Age({ figures }: { figures: LoopFigures }) {
  const now = useMinuteClock()
  const age = figuresAge(figures, now)
  if (!age) return null
  return <span className={`figures-age${age.stale ? ' stale' : ''}`}>{age.text}</span>
}

function Body({ figures }: { figures: LoopFigures }) {
  const line = figuresLine(figures)
  const f = figures.figures
  if (line || !f) {
    return (
      <p className="figures-empty">
        {line}
        {figures.langfuse === 'off' && (
          <>
            {' '}
            <a href={LANGFUSE_DOCS} target="_blank" rel="noreferrer">
              Langfuse beside the hub
            </a>{' '}
            says how to turn them on.
          </>
        )}
      </p>
    )
  }
  const each = perTurn(f)
  return (
    <>
      <div className="figures-tiles">
        <Tile label="Turns" value={String(f.turns)}>
          {f.failed_turns > 0 ? (
            <span className="figures-failed">{f.failed_turns} failed</span>
          ) : (
            'none failed'
          )}
        </Tile>
        <Tile label="Spend, Langfuse's" value={formatUsd(f.cost_usd)}>
          {formatUsd(each.cost)} a turn
        </Tile>
        <Tile label="Tokens a turn" value={`${formatTokens(each.input)} in`}>
          {formatTokens(each.output)} out · {formatTokens(each.cacheRead)} cache read
        </Tile>
        <Tile label="Turn duration" value={formatDuration(f.turn_duration_ms.p50)}>
          median · {formatDuration(f.turn_duration_ms.p95)} p95 · {formatDuration(f.turn_duration_ms.max)} max
        </Tile>
      </div>
      {f.per_turn.length > 0 && <Turns turns={f.per_turn} />}
      <div className="figures-lists">
        <div className="figures-list">
          <span className="figures-label">Tools</span>
          {f.tools.length === 0 && <span className="figures-none">no tool calls</span>}
          {toolMix(f.tools).map((t) => (
            <div key={t.name} className={`figures-tool${t.other ? ' other' : ''}`}>
              <span className="figures-tool-name" title={t.name}>
                {t.name}
              </span>
              <span className="figures-bar" aria-hidden="true">
                <span style={{ width: `${t.share * 100}%` }} />
              </span>
              <span className="figures-num">{t.calls}</span>
              <span className="figures-num figures-failed">{t.failures > 0 && `${t.failures} failed`}</span>
            </div>
          ))}
        </div>
        <div className="figures-list">
          <span className="figures-label">Failures</span>
          {f.failures.length === 0 && <span className="figures-none">none</span>}
          {f.failures.map((x) => (
            <div key={x.class} className="figures-failure">
              <span className="figures-failed">{x.class}</span>
              <span className="figures-num">{x.count}</span>
            </div>
          ))}
        </div>
      </div>
    </>
  )
}

function Tile({ label, value, children }: { label: string; value: string; children: ReactNode }) {
  return (
    <div className="figures-tile">
      <span className="figures-label">{label}</span>
      <span className="figures-value">{value}</span>
      <span className="figures-sub">{children}</span>
    </div>
  )
}

// Each turn's spend as a bar, oldest first, a failed turn's in red. In the
// detail, the line under the bars reads the one under the pointer, or the
// newest.
function Bars({
  turns,
  at,
  onAt,
}: {
  turns: FiguresTurn[]
  at?: number | null
  onAt?: (i: number | null) => void
}) {
  const shares = turnShares(turns)
  return (
    <span className="figures-turn-bars" aria-hidden="true" onMouseLeave={onAt && (() => onAt(null))}>
      {turns.map((turn, i) => (
        <span
          key={`${turn.started_at}-${i}`}
          className={`figures-turn${turn.failed ? ' failed' : ''}${i === at ? ' at' : ''}`}
          onMouseEnter={onAt && (() => onAt(i))}
        >
          <span style={{ height: `${Math.max(shares[i] * 100, 3)}%` }} />
        </span>
      ))}
    </span>
  )
}

function Turns({ turns }: { turns: FiguresTurn[] }) {
  const [at, setAt] = useState<number | null>(null)
  const t = turns[turnAt(turns.length, at)]
  return (
    <div className="figures-turns">
      <span className="figures-label">Spend a turn · {count(turns.length, 'turn')}</span>
      <Bars turns={turns} at={at} onAt={setAt} />
      <span className="figures-turn-line">
        {messageTime(t.started_at)} · {formatUsd(t.cost_usd)} · {formatDuration(t.duration_ms)} ·{' '}
        {formatTokens(t.input_tokens)} in · {formatTokens(t.output_tokens)} out ·{' '}
        {count(t.tool_calls, 'tool call')}
        {t.failed && <span className="figures-failed"> · failed</span>}
      </span>
    </div>
  )
}
