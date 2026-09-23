import { useLayoutEffect, useRef, useState, type KeyboardEvent, type UIEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ChatMessage, LoopView } from '../api'
import { channelRecipients, completeMention, mentionAt, mentionCompletions } from '../channel'
import { MessageKnot } from './MessageKnot'

// The fleet channel: the conversation the operator and the loops share,
// which lives on the hub and is native to the control room (ADR-0032, #286).
//
// The one conversation that belongs to no loop, so it is not a pane of a
// loop's page; it is the fleet's, so it is a tab of the Fleet page rather
// than a destination of its own. Not a part of Activity: that is a read-only
// log of every conversation, owner DMs included (ADR-0025 item 9), and a
// compose box there would sit beside them.
//
// Nothing posted here leaves the hub (ADR-0032 item 4). An attached surface
// brings its room's posts in, so people on Telegram appear in this timeline,
// but they never see what the operator writes here. The compose box says
// so, because a timeline with humans in it looks like a place they can be
// answered.

// Where a message came from, for the ones whose author alone does not say
// it: a human's post arrived through a surface, and naming the surface is
// how the operator learns this person cannot read a reply written here.
//
// The operator's own posts are not marked "hub only" from their origin:
// before #293 a composer post to the group did go out to Telegram, so the
// origin cannot say whether one stayed. The message's mirror state (#285)
// will; until then the compose box carries the rule.
function originNote(m: ChatMessage): string {
  return m.origin === 'telegram-group' ? ' · via Telegram' : ''
}

// The loops the hub delivered a message to — the message's `delivered_to`.
// Hub delivery, not surface delivery: a person on Telegram is never in this
// list, and a loop's failure to reach the surface is the undelivered mark's
// to say.
//
// "Reached no loop" is said only for a person's post, where it answers the
// question the reader has — did anyone hear this? A loop's post that
// addresses no other loop is the ordinary case (a reply to a person wakes
// nobody, ADR-0025), and a line saying so under every one of them would sit
// next to the undelivered mark and read like a second failure.
//
// The field holds loop ids, so they are named from the loop list; an id no
// loop has any more (a deleted loop) is shown as it is rather than dropped,
// because it did reach something.
function Reached({ msg, loops }: { msg: ChatMessage; loops: LoopView[] }) {
  const names = (msg.delivered_to ?? []).map((id) => loops.find((l) => l.id === id)?.name ?? id)
  if (names.length === 0 && msg.origin === 'loop') return null
  return (
    <div
      className="reached"
      title="The loops the hub delivered this to. People on an attached surface are not listed: nothing here tells them."
    >
      {names.length > 0 ? `reached ${names.map((n) => `@${n}`).join(', ')}` : 'reached no loop'}
    </div>
  )
}

// What a reply answers, drawn above it. The message it points at is usually
// in the window; when it has scrolled out, the quote says so rather than
// dropping the fact that this is a reply.
function ReplyQuote({ msg, byID }: { msg: ChatMessage; byID: Map<number, ChatMessage> }) {
  if (!msg.reply_to_id) return null
  const target = byID.get(msg.reply_to_id)
  if (!target) return <div className="reply-quote">replying to an earlier message</div>
  return (
    <div className="reply-quote" title={target.text}>
      <span className="author">@{target.author}</span> {target.text}
    </div>
  )
}

function Compose({ loops }: { loops: LoopView[] }) {
  const qc = useQueryClient()
  const [draft, setDraft] = useState('')
  const [caret, setCaret] = useState(0)
  const [picked, setPicked] = useState(0)
  const [dismissed, setDismissed] = useState(false)
  const [error, setError] = useState('')
  const [sending, setSending] = useState(false)
  const box = useRef<HTMLTextAreaElement>(null)
  // Where the caret goes once a completion's text is in the box. Set in a
  // layout effect, before the browser paints or takes another keystroke: a
  // frame later, a fast typist's next characters have already landed at
  // the end of the text and the caret jumps back into the middle of them.
  const pendingCaret = useRef<number | null>(null)
  useLayoutEffect(() => {
    const at = pendingCaret.current
    if (at === null || !box.current) return
    box.current.setSelectionRange(at, at)
    pendingCaret.current = null
  }, [draft])

  const typing = dismissed ? null : mentionAt(draft, caret)
  const options = typing ? mentionCompletions(typing.query, loops) : []
  const recipients = channelRecipients(draft, loops)
  const empty = draft.trim() === ''

  const track = (text: string, at: number) => {
    setDraft(text)
    setCaret(at)
    setPicked(0)
    setDismissed(false)
  }

  const choose = (name: string) => {
    if (!typing) return
    const next = completeMention(draft, typing.start, caret, name)
    pendingCaret.current = next.caret
    track(next.text, next.caret)
    box.current?.focus()
  }

  const send = async () => {
    // Refused here rather than sent to nobody. The server keeps a post that
    // addresses nobody, as it keeps a human's, but from the operator it is
    // almost always a forgotten name, and the loop send tool refuses the
    // same case (`no_recipients`).
    if (empty || recipients.length === 0 || sending) return
    setSending(true)
    setError('')
    try {
      await api.postGroup(draft.trim())
      track('', 0)
      qc.invalidateQueries({ queryKey: ['group'] })
    } catch (e) {
      // The draft stays: a post the server refused is still the operator's
      // words, and clearing it would make them type it again.
      setError(e instanceof Error ? e.message : 'could not post to the fleet channel')
    } finally {
      setSending(false)
    }
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (options.length > 0) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        const step = e.key === 'ArrowDown' ? 1 : -1
        setPicked((i) => (i + step + options.length) % options.length)
        return
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
        e.preventDefault()
        choose(options[picked] ?? options[0])
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setDismissed(true)
        return
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  return (
    <div className="channel-compose">
      {options.length > 0 && (
        <div className="mention-menu" role="listbox" aria-label="Loops in the fleet channel">
          {options.map((name, i) => (
            <button
              key={name}
              type="button"
              role="option"
              aria-selected={i === picked}
              className={`mention-option${i === picked ? ' on' : ''}`}
              // mousedown, not click: a click would blur the box first and
              // lose the caret the completion is inserted at.
              onMouseDown={(e) => {
                e.preventDefault()
                choose(name)
              }}
            >
              @{name}
              {name === 'all' && <span className="mention-hint"> every active loop in the channel</span>}
            </button>
          ))}
        </div>
      )}
      <div className="composer" style={{ marginTop: 0 }}>
        <textarea
          ref={box}
          placeholder="Post to the fleet channel — @name the loops it is for, or @all"
          value={draft}
          onChange={(e) => track(e.target.value, e.target.selectionStart)}
          onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
          onKeyDown={onKeyDown}
        />
        <button className="btn primary" onClick={send} disabled={empty || recipients.length === 0 || sending}>
          Send
        </button>
      </div>
      {/* Neutral while a mention is still being typed: "reaches nobody" in
          the warning colour under a half-typed name is a complaint about a
          keystroke, not about the post. */}
      <div className={`channel-reach${!empty && !typing && recipients.length === 0 ? ' warn' : ''}`}>
        {empty || (typing && recipients.length === 0)
          ? 'Reaches the loops you name. Stays on the hub: people on Telegram never see it.'
          : recipients.length === 0
            ? 'Names no loop in the channel, so it would reach nobody. Mention one with @, or @all.'
            : `Reaches ${recipients.map((n) => `@${n}`).join(', ')}. Stays on the hub: people on Telegram never see it.`}
      </div>
      {error && <div className="form-error">{error}</div>}
    </div>
  )
}

export function FleetChannel() {
  const {
    data: msgs,
    isLoading,
    isError,
    error,
  } = useQuery({
    queryKey: ['group'],
    queryFn: () => api.group(),
    refetchInterval: 5000,
  })
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const scroller = useRef<HTMLDivElement>(null)
  // Follow the tail unless the reader has scrolled up to read; the same rule
  // as a loop's panes, with a couple of lines of slack.
  const following = useRef(true)

  const thread = [...(msgs ?? [])].reverse()
  const byID = new Map(thread.map((m) => [m.id, m]))

  useLayoutEffect(() => {
    const el = scroller.current
    if (el && following.current) el.scrollTop = el.scrollHeight
  }, [msgs])

  const onScroll = (e: UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget
    following.current = el.scrollHeight - el.scrollTop - el.clientHeight < 32
  }

  return (
    <div className="fleet-channel">
      {isLoading && <div className="fleet-note">Loading…</div>}
      {isError && (
        <div className="form-error">
          Could not load the fleet channel: {error instanceof Error ? error.message : String(error)}
        </div>
      )}

      <div className="pane-scroll" ref={scroller} onScroll={onScroll}>
        {msgs && msgs.length === 0 ? (
          <div className="empty">Nothing in the fleet channel yet. Name a loop below to start it.</div>
        ) : (
          <div className="timeline">
            {thread.map((m) => (
              <MessageKnot
                key={m.id}
                msg={m}
                meta={originNote(m)}
                before={<ReplyQuote msg={m} byID={byID} />}
                after={<Reached msg={m} loops={loops ?? []} />}
              />
            ))}
          </div>
        )}
      </div>

      <Compose loops={loops ?? []} />
    </div>
  )
}
