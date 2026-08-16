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
  | { kind: 'result'; id: number; ts: number; costUsd: number; durationMs: number; inTok: number; outTok: number; isError: boolean }
  | { kind: 'note'; id: number; ts: number; text: string }

// splitEnvelope separates the bracketed header from the body of an injected
// message ("[message from @x via web · ...]\n\nbody").
function splitEnvelope(text: string): { who: string; body: string } {
  const m = text.match(/^\[([^\]]+)\]\n\n([\s\S]*)$/)
  if (!m) return { who: '', body: text }
  return { who: m[1], body: m[2] }
}

export function toEntries(events: LoopEvent[]): Entry[] {
  const out: Entry[] = []
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
          out.push({
            kind: 'result',
            id: e.id,
            ts: e.ts,
            costUsd: r.total_cost_usd ?? 0,
            durationMs: r.duration_ms ?? 0,
            inTok: r.usage?.input_tokens ?? 0,
            outTok: r.usage?.output_tokens ?? 0,
            isError: !!r.is_error,
          })
          break
        }
        case 'spool': {
          const noteText = spoolNote(e)
          if (noteText) out.push({ kind: 'note', id: e.id, ts: e.ts, text: noteText })
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

function spoolNote(e: LoopEvent): string {
  try {
    const p = JSON.parse(e.payload)
    switch (e.subtype) {
      case 'proc_spawn':
        return p.resume ? 'session resumed' : 'new session started'
      case 'proc_exit':
        return '' // routine; the spawn note is enough
      case 'crash':
        return `process crashed (exit ${p.code})`
      case 'session_lost':
        return 'previous session lost — continuing fresh with mission restated'
      case 'storm_drop':
        return `storm guard: message to @${p.to} dropped (limit ${p.limit_per_hour}/h)`
      default:
        return ''
    }
  } catch {
    return ''
  }
}

// extractDelta pulls streaming text out of a raw stream_event payload.
export function extractDelta(payload: any): string {
  if (payload?.type !== 'stream_event') return ''
  const ev = payload.event
  if (ev?.type === 'content_block_delta' && ev.delta?.type === 'text_delta') {
    return ev.delta.text ?? ''
  }
  return ''
}
