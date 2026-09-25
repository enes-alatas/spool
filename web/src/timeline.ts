// Turns raw loop events into renderable timeline entries.
import type { LoopEvent } from './api'

export interface ContentBlock {
  type: string
  text?: string
  thinking?: string
  name?: string
  input?: unknown
}

export type Entry =
  | { kind: 'inbound'; id: number; ts: number; who: string; body: string; trigger: string }
  | { kind: 'assistant'; id: number; ts: number; blocks: ContentBlock[] }
  | {
      kind: 'result'
      id: number
      ts: number
      costUsd: number
      // 'turn' when costUsd is what this turn spent; 'session' when the
      // turns before it in its session are not loaded, so the only true
      // figure available is the session's running total.
      costBasis: CostBasis
      durationMs: number
      inTok: number
      outTok: number
      isError: boolean
    }
  | { kind: 'note'; id: number; ts: number; text: string; seam: boolean }

// splitEnvelope separates the bracketed header from the body of an injected
// message ("[message from @x via web · ...]\n\nbody").
function splitEnvelope(text: string): { who: string; body: string } {
  const m = text.match(/^\[([^\]]+)\]\n\n([\s\S]*)$/)
  if (!m) return { who: '', body: text }
  return { who: m[1], body: m[2] }
}

export type CostBasis = 'turn' | 'session'

// The CLI reports `total_cost_usd` as the session's running total, not the
// turn's own spend (#191), so a turn costs the increment over the highest
// total its session has reported so far — clamped at zero, because a total
// that failed to grow (an errored turn repeats the previous figure) means the
// turn spent nothing, never that it earned money back. This is the rule the
// server applies to the `turns` column; the transcript reads raw engine
// events and has to apply it itself.
//
// The baseline has to be found in the window, and a transcript is a window:
// it opens on the newest events and walks backwards. When a session's earlier
// turns are not loaded there is no baseline to subtract, and a guessed one
// would misprice the turn — so those turns report the running total and say
// so. The exception is a session that starts inside the window: a spawn that
// did not resume opens a session at zero, which makes its first turn's total
// its own cost.
//
// `hole` is the stretch the window jumped over (LoopDetail's `record.hole`):
// events the page was never handed, sitting between two it holds. It matters
// because evidence on the far side of a hole is not evidence about this turn
// — a baseline read across one is missing however many turns fell in, so the
// increment over it is their spend plus this turn's, and a spawn read across
// one opened a session whose first turns are gone. Both would print a wrong
// figure under the confident `turn` label, which is the case the `session`
// label exists to prevent, so a baseline that spans the hole is discarded
// rather than trusted.
export function toEntries(events: LoopEvent[], hole?: { from: number; to: number } | null): Entry[] {
  const out: Entry[] = []
  // session id -> the highest total that session has reported, and the id of
  // the event that reported it. The id is what lets a later turn ask whether
  // the hole sits between them.
  const reported = new Map<string, { total: number; at: number }>()
  // The id of a fresh spawn whose session has not reported yet: the session it
  // opened starts from zero.
  let opensAtZero: number | null = null
  // Whether the window's missing stretch lies between these two events.
  const spansHole = (from: number, to: number) =>
    hole !== undefined && hole !== null && from <= hole.from && to >= hole.to
  for (const e of events) {
    try {
      switch (e.type) {
        case 'envelope': {
          const env = JSON.parse(e.payload)
          // batched envelopes are stored individually; each shows separately
          const { who, body } = splitEnvelope(env.text ?? '')
          out.push({ kind: 'inbound', id: e.id, ts: e.ts, who, body, trigger: env.trigger ?? '' })
          break
        }
        case 'assistant': {
          const line = JSON.parse(e.payload)
          const blocks: ContentBlock[] = line?.message?.content ?? []
          if (blocks.length > 0) out.push({ kind: 'assistant', id: e.id, ts: e.ts, blocks })
          break
        }
        case 'result': {
          const r = JSON.parse(e.payload)
          const total: number = r.total_cost_usd ?? 0
          let basis: CostBasis = 'turn'
          let spent = total
          const before = reported.get(e.session_id)
          if (before !== undefined && !spansHole(before.at, e.id)) {
            spent = Math.max(0, total - before.total)
          } else if (before === undefined && opensAtZero !== null && !spansHole(opensAtZero, e.id)) {
            opensAtZero = null
          } else {
            basis = 'session'
            opensAtZero = null
          }
          reported.set(e.session_id, { total: Math.max(before?.total ?? 0, total), at: e.id })
          out.push({
            kind: 'result',
            id: e.id,
            ts: e.ts,
            costUsd: spent,
            costBasis: basis,
            durationMs: r.duration_ms ?? 0,
            inTok: r.usage?.input_tokens ?? 0,
            outTok: r.usage?.output_tokens ?? 0,
            isError: !!r.is_error,
          })
          break
        }
        case 'spool': {
          // Not keyed to the spawn's own session id: a fresh spawn is stored
          // under the id Spool minted for the session, which the CLI's init
          // line may then replace with its own (actor.go `handleEvent`), so
          // the results that follow can carry a different id from the spawn
          // that opened them.
          if (e.subtype === 'proc_spawn') {
            opensAtZero = JSON.parse(e.payload).resume ? null : e.id
          }
          const note = spoolNote(e)
          if (note) out.push({ kind: 'note', id: e.id, ts: e.ts, ...note })
          break
        }
        // system/init, hooks, rate limits: not part of the conversation
      }
    } catch {
      /* skip malformed rows */
    }
  }
  return out
}

// A note the room shows for one engine event, and whether it marks a seam:
// the point where the conversation above it and the conversation below it
// belong to two different claude sessions. A seam is not severity — a
// deliberate context rotation is healthy — it is discontinuity, which a
// reader scrolling a thread cannot otherwise see.
interface Note {
  text: string
  seam: boolean
}

// spoolNote is the room's copy for the engine's own events. Every subtype
// `storeSpoolEvent` emits belongs here: the default is to render nothing, so
// an unlisted subtype is not terse, it is invisible.
//
// The copy says what the event cost the loop rather than what the engine did,
// because that is the part a reader cannot reconstruct from the thread — the
// mechanism is in the logs and the payload, the consequence is not.
function spoolNote(e: LoopEvent): Note | null {
  try {
    const p = JSON.parse(e.payload)
    switch (e.subtype) {
      case 'proc_spawn':
        return plain(p.resume ? 'session resumed' : 'new session started')
      case 'proc_exit':
        return null // routine; the spawn note is enough
      case 'crash':
        return plain(`process crashed (exit ${p.code})`)
      case 'retry_scheduled':
        return plain(`retrying in ${formatWait(p.in_ms)}`)
      case 'resume_failed':
        // not a seam: the session is still the loop's, and attempt 1 of 2
        // usually resumes on the retry
        return plain(`session did not load (attempt ${p.attempt}), retrying`)
      case 'session_lost':
        return seam('previous session lost; continuing fresh with mission restated')
      case 'session_unusable':
        return seam('session would not load twice running; continuing fresh with mission restated')
      case 'session_forgotten':
        return seam('workstation rebuilt; the session went with it, and nothing above is remembered')
      case 'context_rotated':
        return seam(rotationNote(p.fill_pct))
      case 'workstation_power':
        return plain(powerNote(p.verb, !!p.ok))
      case 'workstation_down':
        return plain(`workstation unreachable: ${p.detail}`)
      case 'workstation_up':
        return plain('workstation reachable again')
      case 'message_too_long':
        return plain(
          `message too long for the model's context window ` +
            `(${p.messages} messages, ${p.chars.toLocaleString('en-US')} characters); not delivered`,
        )
      case 'model_unrecognized':
        return plain(
          `${p.model ? `model ${p.model}` : 'the default model'} not recognized; ` +
            'the loop holds until its model is changed',
        )
      case 'model_retry':
        // With refused, the refusal was about a model the operator had
        // already replaced mid-turn, so the turn runs again on the new one.
        // Without it, an edit released a held loop, which checks the new
        // model with a turn now.
        if (p.refused) {
          return plain(`model ${p.refused} was replaced mid-turn; retrying on ${p.model || 'the default'}`)
        }
        return plain(`model changed to ${p.model || 'the default'}; checking it with a turn now`)
      case 'storm_drop':
        return plain(`storm guard: message to @${p.to} dropped (limit ${p.limit_per_hour}/h)`)
      case 'send_failed':
        // The loop believes it spoke; this note is the only place the
        // operator learns otherwise — so it says who never heard it, not
        // which chat id the send was aimed at. plain, not seam: nothing
        // above this is forgotten, the words simply never left the machine.
        return plain(`message to ${p.chat} never delivered (${p.attempts} attempts)`)
      default:
        return null
    }
  } catch {
    return null
  }
}

function plain(text: string): Note {
  return { text, seam: false }
}

function seam(text: string): Note {
  return { text, seam: true }
}

// rotationNote reports a deliberate rotation. The occupancy leads when there
// is one, because it is the reason; a rotation asked for before the session
// was ever measured carries `fill_pct: 0`, and "context 0% full" would read
// as a contradiction of the thing it is explaining. The consequence is the
// same either way, so it is the sentence that always survives.
function rotationNote(fillPct: number): string {
  const consequence = 'rotated onto a fresh session; the turns above are not carried'
  return fillPct > 0 ? `context ${fillPct}% full; ${consequence}` : consequence
}

// powerNote names what the operator's control did to the machine. A verb that
// failed left the workstation as it was, so it is phrased as the attempt it
// was, not as a state the reader should believe.
function powerNote(verb: string, ok: boolean): string {
  switch (verb) {
    case 'poweroff':
      return ok ? 'workstation switched off' : 'could not switch the workstation off'
    case 'poweron':
      return ok ? 'workstation switched on' : 'could not switch the workstation on'
    case 'restart':
      return ok ? 'workstation restarted' : 'could not restart the workstation'
    case 'recreate':
      return ok ? 'workstation rebuilt from scratch' : 'could not rebuild the workstation'
    default:
      return ok ? `workstation ${verb}` : `workstation ${verb} failed`
  }
}

// formatWait renders a retry delay the way the ladder reads: seconds while
// it is short, minutes once it has climbed.
function formatWait(ms: number): string {
  const seconds = Math.round(ms / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.round(seconds / 60)
  return `${minutes}m`
}

// extractDelta pulls streaming text out of a raw stream_event payload.
export function extractDelta(payload: unknown): string {
  const p = payload as {
    type?: string
    event?: { type?: string; delta?: { type?: string; text?: string } }
  }
  if (p?.type !== 'stream_event') return ''
  const ev = p.event
  if (ev?.type === 'content_block_delta' && ev.delta?.type === 'text_delta') {
    return ev.delta.text ?? ''
  }
  return ''
}
