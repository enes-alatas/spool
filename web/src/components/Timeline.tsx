import { useState } from 'react'
import type { Entry, ContentBlock } from '../timeline'

function fmtTime(ts: number) {
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function ToolLine({ block }: { block: ContentBlock }) {
  let arg = ''
  try {
    const input = block.input as Record<string, unknown>
    arg = String(input?.command ?? input?.file_path ?? input?.path ?? input?.pattern ?? input?.url ?? '')
  } catch {
    /* no arg preview */
  }
  if (arg.length > 90) arg = arg.slice(0, 90) + '…'
  return (
    <div className="tool-line">
      ⚙ <span className="tool-name">{block.name}</span> {arg}
    </div>
  )
}

function Thinking({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div>
      <button className="text-button" onClick={() => setOpen(!open)}>
        {open ? 'hide thinking' : 'thinking…'}
      </button>
      {open && <div className="thinking-body">{text}</div>}
    </div>
  )
}

function AssistantEntry({ entry }: { entry: Entry & { kind: 'assistant' } }) {
  return (
    <div className="knot" data-entry-id={entry.id}>
      <div className="who">{fmtTime(entry.ts)}</div>
      {entry.blocks.map((b, i) => {
        if (b.type === 'text' && b.text?.trim()) {
          return (
            <div key={i} className="plain">
              {b.text}
            </div>
          )
        }
        if (b.type === 'tool_use') return <ToolLine key={i} block={b} />
        if (b.type === 'thinking' && b.thinking?.trim()) return <Thinking key={i} text={b.thinking} />
        return null
      })}
    </div>
  )
}

export function Timeline({
  entries,
  liveText,
  onLoadOlder,
  loadingOlder,
  olderFailed,
}: {
  entries: Entry[]
  liveText: string
  // Absent once the timeline is showing the loop's first event: there is
  // nothing further back to ask for, so the control goes rather than sitting
  // there disabled and inviting a click that does nothing.
  onLoadOlder?: () => void
  loadingOlder?: boolean
  olderFailed?: boolean
}) {
  return (
    <div className="timeline">
      {onLoadOlder && entries.length > 0 && (
        <div className="load-older-row">
          <button className="load-older" onClick={onLoadOlder} disabled={loadingOlder}>
            {loadingOlder ? 'loading…' : 'load older'}
          </button>
          {/* Said here rather than logged: a button that re-enables itself with
              the history unmoved reads exactly like reaching the first event. */}
          {olderFailed && <span className="load-older-failed">could not reach the store</span>}
        </div>
      )}
      {entries.map((e) => {
        switch (e.kind) {
          case 'inbound':
            return (
              <div key={e.id} className="knot inbound" data-entry-id={e.id}>
                <div className="who">
                  <span className="author">{e.who || e.trigger}</span> · {fmtTime(e.ts)}
                </div>
                <div className="bubble">{e.body}</div>
              </div>
            )
          case 'assistant':
            return <AssistantEntry key={e.id} entry={e} />
          case 'result':
            return (
              <div key={e.id} className="knot reply" style={{ marginTop: -8 }} data-entry-id={e.id}>
                <div className="turn-footer">
                  {e.isError && <span className="err">turn errored</span>}
                  {/* A turn priced on the session basis is the oldest one
                      loaded for its session: its own cost cannot be worked
                      out until the turns above it arrive, so the footer says
                      which figure it is showing rather than passing the
                      running total off as this turn's. */}
                  {e.costBasis === 'session' ? (
                    <span title="the turns before this one in its session are not loaded, so this is the session's total so far, not this turn's cost">
                      ${e.costUsd.toFixed(4)} session
                    </span>
                  ) : (
                    <span>${e.costUsd.toFixed(4)}</span>
                  )}
                  <span>{(e.durationMs / 1000).toFixed(1)}s</span>
                  <span>
                    {e.inTok}→{e.outTok} tok
                  </span>
                </div>
              </div>
            )
          case 'note':
            return (
              <div
                key={e.id}
                className={e.seam ? 'knot system-note seam' : 'knot system-note'}
                data-entry-id={e.id}
              >
                <div className="who">
                  {e.text} · {fmtTime(e.ts)}
                </div>
              </div>
            )
        }
      })}
      {liveText && (
        <div className="knot">
          <div className="plain">
            {liveText}
            <span className="live-cursor" />
          </div>
        </div>
      )}
    </div>
  )
}
