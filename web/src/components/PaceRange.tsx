import { useState } from 'react'
import { movePace, paceLabel, paceStops, typeTick, type Pace } from '../pace'

// One track, three handles: min wake, the tick interval and max wake (#523).
// Three native range inputs share the track, so each handle keeps the
// browser's keyboard and screen-reader behaviour; only their thumbs take the
// pointer. The handles cannot pass each other (movePace). The tick also takes
// typed minutes, for a pace no stop offers; min and max wake are the stops.
export function PaceRange({
  pace,
  onChange,
  disabled,
  labelledBy,
}: {
  pace: Pace
  onChange: (pace: Pace) => void
  disabled?: boolean
  // The id of a visible label for the whole control; without one it is
  // named "Pace".
  labelledBy?: string
}) {
  const stops = paceStops(pace)
  const last = stops.length - 1
  const at = (seconds: number) => stops.indexOf(seconds)
  const percent = (seconds: number) => (at(seconds) / last) * 100
  const handle = (name: keyof Pace, label: string) => (
    <input
      type="range"
      className={`pace-handle pace-${name}`}
      min={0}
      max={last}
      step={1}
      value={at(pace[name])}
      disabled={disabled}
      aria-label={label}
      aria-valuetext={paceLabel(pace[name])}
      onChange={(e) => onChange(movePace(pace, name, stops[Number(e.target.value)]))}
    />
  )
  return (
    <div
      className="pace-range"
      role="group"
      aria-labelledby={labelledBy}
      aria-label={labelledBy ? undefined : 'Pace'}
    >
      <div className="pace-track">
        {/* A thumb's centre runs from half a thumb in to half a thumb short
            of the end, so the span is laid on that inner length. */}
        <div
          className="pace-span"
          style={{
            left: `calc(7px + (100% - 14px) * ${percent(pace.min) / 100})`,
            width: `calc((100% - 14px) * ${(percent(pace.max) - percent(pace.min)) / 100})`,
          }}
        />
        {handle('min', 'Min wake')}
        {handle('max', 'Max wake')}
        {handle('tick', 'Tick interval')}
      </div>
      <div className="pace-ends">
        <span>{paceLabel(stops[0])}</span>
        <span>{paceLabel(stops[last])}</span>
      </div>
      <div className="pace-legend">
        <span>
          <span className="pace-key pace-key-range" /> min wake <b>{paceLabel(pace.min)}</b>
        </span>
        <span>
          <span className="pace-key pace-key-tick" /> tick{' '}
          <TickBox pace={pace} onChange={onChange} disabled={disabled} />
        </span>
        <span>
          <span className="pace-key pace-key-range" /> max wake <b>{paceLabel(pace.max)}</b>
        </span>
      </div>
    </div>
  )
}

// The tick as typed minutes. It applies on Enter or on leaving the box, held
// between min and max wake, and a value that isn't whole minutes goes back
// to the tick as it was. Outside an edit the box shows the tick, so a moved
// handle rewrites it.
function TickBox({
  pace,
  onChange,
  disabled,
}: {
  pace: Pace
  onChange: (pace: Pace) => void
  disabled?: boolean
}) {
  const [draft, setDraft] = useState<string | null>(null)
  const apply = () => {
    const next = draft === null ? null : typeTick(pace, draft)
    if (next) onChange(next)
    setDraft(null)
  }
  return (
    <span className="pace-tick-box">
      <input
        className="tick-input"
        aria-label="Tick interval in minutes"
        inputMode="numeric"
        value={draft ?? String(Math.round(pace.tick / 60))}
        disabled={disabled}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={apply}
        onKeyDown={(e) => {
          if (e.key === 'Enter') apply()
        }}
      />{' '}
      min
    </span>
  )
}
