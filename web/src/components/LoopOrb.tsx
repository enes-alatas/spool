import { useId, useLayoutEffect, useRef } from 'react'
import { Link } from 'react-router'
import type { LoopView } from '../api'
import { formatUsd, inTurn, nextWake, spendShare } from '../format'
import { problemLine } from '../health'
import { needsYou } from '../teams'
import { useMinuteClock } from './PlanUsage'

// A loop as a circle on the Fleet page (#1411). Its outer ring is how full
// its context is and its inner ring its share of the fleet's spend today; a
// red halo means something is wrong with it, whatever the fault, and a slow
// dashed ring means it is in a turn. Red is kept for faults and orange for
// running, so the context ring climbs from green to a pale blue, never amber.

export const ORB = 64
export const FOCUSED_ORB = 340

// The short names a problem goes by in a circle too small for its sentence.
const SHORT: Record<string, string> = {
  workstation_down: 'workstation down',
  turn_errors: 'turns failing',
  turn_stuck: 'turn stuck',
  wake_missed: 'wake missed',
  send_failed: 'send failed',
}

function contextColor(pct: number): string {
  if (pct >= 70) return 'var(--orb-context-high)'
  if (pct >= 40) return 'var(--orb-context-mid)'
  return 'var(--ready)'
}

// What the circle says under the loop's name: its first problem, else its state.
export function orbLabel(loop: Pick<LoopView, 'state' | 'problems'>): string {
  const problem = loop.problems[0]
  if (problem) return SHORT[problem.symptom] ?? problem.symptom.replace(/_/g, ' ')
  return loop.state.replace(/_/g, ' ')
}

function wakeLine(loop: LoopView, now: number): string {
  if (inTurn(loop.state)) return 'in a turn now'
  const at = nextWake(loop)
  if (!at) return 'no wake set'
  const minutes = Math.max(0, Math.round((at - now) / 60000))
  return minutes < 60 ? `wakes in ${minutes}m` : `wakes in ${Math.round(minutes / 60)}h`
}

function Arc({ r, pct, color, width }: { r: number; pct: number; color: string; width: number }) {
  const circumference = 2 * Math.PI * r
  const p = Math.max(0, Math.min(100, pct))
  return (
    <>
      <circle cx="60" cy="60" r={r} className="orb-track" strokeWidth={width} />
      {p > 0 && (
        <circle
          cx="60"
          cy="60"
          r={r}
          fill="none"
          stroke={color}
          strokeWidth={width}
          strokeLinecap="round"
          strokeDasharray={`${(circumference * p) / 100} ${circumference}`}
          transform="rotate(-90 60 60)"
        />
      )}
    </>
  )
}

function Rings({ loop, spentToday, big }: { loop: LoopView; spentToday: number; big: boolean }) {
  const gradient = useId()
  const context = loop.context_fill_pct ?? 0
  const fault = needsYou(loop)
  return (
    <svg viewBox="0 0 120 120" aria-hidden>
      <defs>
        <radialGradient id={gradient} cx="50%" cy="38%" r="70%">
          <stop offset="0%" className={fault ? 'orb-core-top fault' : 'orb-core-top'} />
          <stop offset="100%" className="orb-core-bottom" />
        </radialGradient>
      </defs>
      {fault && <circle cx="60" cy="60" r="57.5" className="orb-halo" />}
      {inTurn(loop.state) && <circle cx="60" cy="60" r="57.5" className="orb-spin" />}
      <Arc r={big ? 52 : 51} pct={context} color={contextColor(context)} width={big ? 3.5 : 8} />
      <Arc
        r={big ? 45.5 : 41}
        pct={spendShare(loop.cost_today_usd, spentToday)}
        color="var(--text)"
        width={big ? 2 : 5}
      />
      <circle cx="60" cy="60" r={big ? 47 : 35} fill={`url(#${gradient})`} className="orb-core" />
    </svg>
  )
}

// The circle at the head of the loop's row in the Fleet list (#1484): the
// same rings and initial as its card's circle, smaller, and only a picture;
// the row is the link.
export function RowOrb({ loop, spentToday }: { loop: LoopView; spentToday: number }) {
  const fault = needsYou(loop)
  return (
    <span
      className={`orb row-orb${fault ? ' fault' : ''}${!fault && !inTurn(loop.state) ? ' calm' : ''}`}
      aria-hidden
    >
      <Rings loop={loop} spentToday={spentToday} big={false} />
      <span className="orb-initial">{loop.name.charAt(0).toUpperCase()}</span>
    </span>
  )
}

// The circle in its team's card: the loop's initial, and on hover or focus its
// name and what is wrong. A click opens it in the middle of the page.
export function LoopOrb({
  loop,
  spentToday,
  onOpen,
}: {
  loop: LoopView
  spentToday: number
  onOpen: () => void
}) {
  const fault = needsYou(loop)
  return (
    <button
      type="button"
      className={`orb${fault ? ' fault' : ''}${!fault && !inTurn(loop.state) ? ' calm' : ''}`}
      style={{ width: ORB, height: ORB }}
      aria-label={`${loop.name}: ${orbLabel(loop)}${loop.undelivered > 0 ? `, ${loop.undelivered} undelivered` : ''}`}
      onClick={onOpen}
    >
      <Rings loop={loop} spentToday={spentToday} big={false} />
      <span className="orb-initial" aria-hidden>
        {loop.name.charAt(0).toUpperCase()}
      </span>
      <span className="orb-peek" aria-hidden>
        <span className="orb-name">{loop.name}</span>
        <span className={`orb-state${fault ? ' fault' : ''}`}>{orbLabel(loop)}</span>
        {loop.undelivered > 0 && <span className="orb-state fault">{loop.undelivered} undelivered</span>}
      </span>
    </button>
  )
}

const GLIDE = 'cubic-bezier(0.22, 1.15, 0.36, 1)'

function reducedMotion(): boolean {
  return window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
}

// A loop opened from its circle: the circle glides from its place in the card
// to the middle of the page and grows there, over the room dimmed behind it,
// so the cards never move. Escape or a click outside sends it back.
export function FocusedOrb({
  loop,
  spentToday,
  from,
  onClose,
}: {
  loop: LoopView
  spentToday: number
  // Where the circle sat in its card, to glide from and back to.
  from: DOMRect
  onClose: () => void
}) {
  const now = useMinuteClock()
  const dialog = useRef<HTMLDialogElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const veil = useRef<HTMLDivElement>(null)
  const open = useRef<HTMLAnchorElement>(null)
  const closing = useRef(false)
  const fault = needsYou(loop)
  const context = loop.context_fill_pct ?? 0

  const offset = () =>
    `translate(${from.left + from.width / 2 - window.innerWidth / 2}px, ${
      from.top + from.height / 2 - window.innerHeight / 2
    }px) scale(${from.width / FOCUSED_ORB})`

  useLayoutEffect(() => {
    // A native modal dialog, as the room's other dialogs are, so focus stays
    // in it and the page behind is inert while it is open.
    if (dialog.current && !dialog.current.open) dialog.current.showModal()
    open.current?.focus({ preventScroll: true })
    if (reducedMotion() || !box.current?.animate) return
    box.current.animate([{ transform: offset() }, { transform: 'none' }], { duration: 560, easing: GLIDE })
    veil.current?.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 320, easing: 'ease-out' })
    // Played once, from where the circle was when it opened.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const close = () => {
    if (closing.current) return
    closing.current = true
    if (reducedMotion() || !box.current?.animate) return onClose()
    veil.current?.animate([{ opacity: 1 }, { opacity: 0 }], {
      duration: 300,
      easing: 'ease-in',
      fill: 'forwards',
    })
    box.current.animate([{ transform: 'none' }, { transform: offset() }], {
      duration: 380,
      easing: 'cubic-bezier(0.5, 0, 0.75, 0)',
      fill: 'forwards',
    }).onfinish = onClose
  }

  return (
    <dialog
      ref={dialog}
      className="orb-focus"
      aria-label={loop.name}
      // Escape: glide back rather than vanish. Chromium closes the dialog on
      // a second Escape whatever the page says; that close unmounts it at
      // once, without the glide.
      onCancel={(e) => {
        if (e.target !== e.currentTarget) return
        e.preventDefault()
        close()
      }}
      onClose={(e) => e.target === e.currentTarget && onClose()}
      onClick={close}
    >
      <div ref={veil} className="orb-veil" />
      <div
        ref={box}
        className={`orb orb-big${fault ? ' fault' : ''}`}
        style={{ width: FOCUSED_ORB, height: FOCUSED_ORB }}
        onClick={(e) => e.stopPropagation()}
      >
        <Rings loop={loop} spentToday={spentToday} big />
        <div className="orb-detail">
          <b className="orb-detail-name">{loop.name}</b>
          <span className="orb-detail-state">{loop.state.replace(/_/g, ' ')}</span>
          {loop.problems.slice(0, 2).map((problem) => (
            <span key={problem.symptom} className="orb-detail-problem">
              {problemLine(problem, now)}
            </span>
          ))}
          {loop.undelivered > 0 && <span className="orb-detail-problem">{loop.undelivered} undelivered</span>}
          {loop.doing && <span className="orb-detail-doing">{loop.doing}</span>}
          <span className="orb-detail-facts">
            {context}% context · {spendShare(loop.cost_today_usd, spentToday)}% of today ·{' '}
            {formatUsd(loop.cost_today_usd)}
          </span>
          <span className="orb-detail-facts">{wakeLine(loop, now)}</span>
          <Link ref={open} to={`/loops/${loop.name}`} className="orb-detail-open">
            Open loop →
          </Link>
        </div>
      </div>
    </dialog>
  )
}
