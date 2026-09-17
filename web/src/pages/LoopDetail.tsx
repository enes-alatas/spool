import { useState, useRef, useEffect, useMemo } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { api, ChatMessage, LoopView, MessageDestination, Settings, TGSender, Turn } from '../api'
import { formatTokens, fillTone } from '../format'
import { MODEL_OPTIONS, EFFORT_OPTIONS, PACING_OPTIONS } from '../options'
import { useStream } from '../stream'
import { toEntries, extractDelta } from '../timeline'
import { Timeline } from '../components/Timeline'
import { SpoolGlyph } from '../components/Spool'

function Countdown({ at }: { at: number }) {
  const [s, setS] = useState<number | null>(null)
  useEffect(() => {
    const update = () => setS(Math.max(0, Math.floor((at - Date.now()) / 1000)))
    update()
    const t = setInterval(update, 1000)
    return () => clearInterval(t)
  }, [at])
  if (!at || s === null) return <div className="countdown">—</div>
  if (s === 0) return <div className="countdown due">due now</div>
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const txt =
    h > 0
      ? `${h}:${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
      : `${m}:${String(s % 60).padStart(2, '0')}`
  return <div className="countdown">{txt}</div>
}

function ScheduleEditor({
  name,
  current,
}: {
  name: string
  current: { tick: number; min: number; max: number; idle: number }
}) {
  const qc = useQueryClient()
  const [tick, setTick] = useState(String(Math.round(current.tick / 60)))
  const mut = useMutation({
    mutationFn: () => api.patchLoop(name, { tick_interval_sec: Math.max(60, Number(tick) * 60) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['loop', name] }),
  })
  return (
    <div className="row" style={{ alignItems: 'center' }}>
      <span className="k">interval</span>
      <span style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
        <input
          style={{
            width: 56,
            background: 'var(--canvas)',
            border: '1px solid var(--hairline)',
            borderRadius: 5,
            color: 'var(--text)',
            padding: '2px 6px',
            fontFamily: 'var(--mono)',
            fontSize: '0.75rem',
          }}
          value={tick}
          onChange={(e) => setTick(e.target.value)}
        />
        <span className="k">min</span>
        <button className="btn sm" onClick={() => mut.mutate()} disabled={mut.isPending}>
          Set
        </button>
      </span>
    </div>
  )
}

const selectStyle = {
  width: '100%',
  background: 'var(--canvas)',
  border: '1px solid var(--hairline)',
  borderRadius: 5,
  color: 'var(--text)',
  padding: '4px 8px',
  fontSize: '0.78125rem',
} as const

// ModelPanel edits model / effort / pacing in place; changes apply from the
// loop's next wake.
function ModelPanel({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const patch = (body: Record<string, string>) =>
    api.patchLoop(loop.name, body).then(() => {
      qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      qc.invalidateQueries({ queryKey: ['loops'] })
    })

  const knownModel = MODEL_OPTIONS.some((o) => o.value === loop.model)

  return (
    <div className="side-panel">
      <h3>Model & pacing</h3>
      <div className="row" style={{ alignItems: 'center' }}>
        <span className="k">model</span>
      </div>
      <select style={selectStyle} value={loop.model} onChange={(e) => patch({ model: e.target.value })}>
        {MODEL_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
        {!knownModel && <option value={loop.model}>{loop.model}</option>}
      </select>
      <div className="row" style={{ marginTop: 8 }}>
        <span className="k">effort</span>
      </div>
      <select style={selectStyle} value={loop.effort} onChange={(e) => patch({ effort: e.target.value })}>
        {EFFORT_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <div className="row" style={{ marginTop: 8 }}>
        <span className="k">pacing</span>
      </div>
      <select style={selectStyle} value={loop.pacing} onChange={(e) => patch({ pacing: e.target.value })}>
        {PACING_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <div style={{ fontSize: '0.71875rem', color: 'var(--text-muted)', marginTop: 8 }}>
        Changes apply from the next wake.
      </div>
    </div>
  )
}

// Context occupancy the turn measured at its last API call — what the next
// prompt would carry into the window. Turns recorded before the measure
// existed read 0: unmeasured, not empty.
function contextTokens(turn: Turn): number {
  return turn.context_tokens
}

// ContextFill is the occupancy of the model's own window — the glanceable
// number. Only rendered when Spool knows the window; an unknown limit gets
// absolute tokens rather than a percentage against a guess.
function ContextFill({
  tokens,
  limit,
  pct,
  thresholds,
}: {
  tokens: number
  limit: number
  pct: number
  thresholds?: Settings
}) {
  return (
    <div className={`ctx-fill ${fillTone(pct, thresholds)}`} title={`${tokens} of ${limit} tokens`}>
      <div className="ctx-fill-bar" style={{ width: `${pct}%` }} />
      <span className="ctx-fill-pct">{pct}%</span>
    </div>
  )
}

// ContextPanel plots context occupancy across recent turns so a loop climbing
// toward its window is visible before it overflows. Bars are relative to the
// largest turn in view; the fill against the model's own window is the row
// above them, and only when Spool knows that window.
function ContextPanel({ loop, turns, thresholds }: { loop: LoopView; turns: Turn[]; thresholds?: Settings }) {
  const finished = turns.filter((turn) => turn.ended_at > 0).reverse()
  if (finished.length === 0) return null

  const peak = Math.max(...finished.map(contextTokens))
  const latest = contextTokens(finished[finished.length - 1])

  return (
    <div className="side-panel">
      <h3>Context</h3>
      <div className="row">
        <span className="k">last turn</span>
        <span className="v">
          {formatTokens(loop.context_tokens || latest)}
          {loop.context_limit_tokens > 0 && ` / ${formatTokens(loop.context_limit_tokens)}`} tokens
        </span>
      </div>
      {loop.context_limit_tokens > 0 && (
        <ContextFill
          tokens={loop.context_tokens}
          limit={loop.context_limit_tokens}
          pct={loop.context_fill_pct}
          thresholds={thresholds}
        />
      )}
      <div className="ctx-trend">
        {finished.map((turn) => (
          <div
            key={turn.id}
            className="ctx-bar"
            style={{ height: `${peak > 0 ? (contextTokens(turn) / peak) * 100 : 0}%` }}
            title={`${new Date(turn.started_at).toLocaleTimeString()} — ${formatTokens(
              contextTokens(turn),
            )} tokens in context`}
          />
        ))}
      </div>
      <div style={{ fontSize: '0.71875rem', color: 'var(--text-muted)', marginTop: 8 }}>
        Context occupancy at each turn's last API call, oldest first. Bars are relative to the tallest turn
        shown.
      </div>
    </div>
  )
}

type WorkstationVerb = 'restart' | 'poweroff' | 'poweron' | 'recreate'

// What to call a verb while it is still running. A cold recreate can take
// minutes, so the room says what is happening rather than freezing a button.
const VERB_PROGRESS: Record<string, string> = {
  restart: 'Restarting…',
  poweroff: 'Powering off…',
  poweron: 'Powering on…',
  recreate: 'Recreating…',
}

// WorkstationPanel shows where a loop's claude actually runs (ADR-0017) — the
// container or the bare host — and whether that machine is reachable right now.
function WorkstationPanel({ loop, runningVerb }: { loop: LoopView; runningVerb: string }) {
  const qc = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const [typed, setTyped] = useState('')
  const [error, setError] = useState('')
  const contained = loop.runtime === 'docker'

  // The response carries the loop as it is after the verb, so it seeds the
  // cache directly instead of costing a refetch.
  const power = useMutation({
    mutationFn: (verb: WorkstationVerb) => api.workstationPower(loop.name, verb),
    onSuccess: (updated) => {
      setError('')
      qc.setQueryData(['loop', loop.name], updated)
      qc.invalidateQueries({ queryKey: ['loops'] })
    },
    onError: (err: unknown) => setError(err instanceof Error ? err.message : String(err)),
  })

  // The verb the server says is under way outranks our own pending call: it
  // survives a dropped connection, which a ten-minute recreate may not.
  const busy = power.isPending || runningVerb !== ''
  const poweredOff = !loop.workstation_up && loop.down_reason === 'powered_off'
  const status = loop.workstation_up ? 'up' : poweredOff ? 'powered off' : 'down'
  // A workstation the operator switched off is not a fault: it reads calm,
  // while one that died on its own keeps the alarm.
  const statusDot = loop.workstation_up
    ? 'state-idle'
    : poweredOff
      ? 'state-asleep'
      : 'state-workstation_down'

  const recreate = () => {
    setConfirming(false)
    setTyped('')
    power.mutate('recreate')
  }

  return (
    <div className="side-panel">
      <h3>Workstation</h3>
      <div className="row" style={{ alignItems: 'center' }}>
        <span className="k">status</span>
        <span className="v" style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span className={`state-dot ${statusDot}`} />
          {status}
        </span>
      </div>
      <div className="row">
        <span className="k">runtime</span>
        <span className="v">{loop.runtime}</span>
      </div>
      {contained ? (
        <>
          <div className="row">
            <span className="k">image</span>
            <span className="v">{loop.image || 'server default'}</span>
          </div>
          <div className="row">
            <span className="k">resources</span>
            <span className="v">
              {loop.mem_mb} MB · {loop.cpus} cpu
            </span>
          </div>
          <div className="controls" style={{ marginTop: 12 }}>
            <button className="btn sm" onClick={() => power.mutate('restart')} disabled={busy}>
              Restart
            </button>
            {loop.workstation_up ? (
              <button className="btn sm" onClick={() => power.mutate('poweroff')} disabled={busy}>
                Power off
              </button>
            ) : (
              <button className="btn sm" onClick={() => power.mutate('poweron')} disabled={busy}>
                Power on
              </button>
            )}
            <button
              className="btn sm danger"
              onClick={() => setConfirming(true)}
              disabled={busy || confirming}
            >
              Recreate
            </button>
          </div>
          {confirming && (
            <div className="ws-confirm">
              <div>
                Recreating destroys this workstation and builds a fresh one from its image.
                <strong> Gone:</strong> the loop's session memory — it starts its next turn fresh, with no
                recollection of this conversation — plus anything uncommitted in the workspace and anything it
                installed. <strong>Kept:</strong> its mission, schedule, secrets, Spool's own record of every
                message and turn, and anything it pushed to a remote.
              </div>
              <div style={{ marginTop: 8 }}>
                Type <code>{loop.name}</code> to confirm:
              </div>
              <input
                value={typed}
                onChange={(event) => setTyped(event.target.value)}
                style={{ fontFamily: 'var(--mono)', marginTop: 6 }}
              />
              <div className="controls" style={{ marginTop: 8 }}>
                <button className="btn sm danger" onClick={recreate} disabled={typed !== loop.name}>
                  Recreate workstation
                </button>
                <button
                  className="btn sm"
                  onClick={() => {
                    setConfirming(false)
                    setTyped('')
                  }}
                >
                  Cancel
                </button>
              </div>
            </div>
          )}
          {runningVerb && (
            <div className="ws-progress">{VERB_PROGRESS[runningVerb] ?? `${runningVerb}…`}</div>
          )}
          {error && <div className="form-error">{error}</div>}
        </>
      ) : (
        <div style={{ fontSize: '0.71875rem', color: 'var(--text-muted)', marginTop: 8 }}>
          Uncontained — claude runs directly on the host, with the operator's own files in reach. There is no
          workstation to power.
        </div>
      )}
    </div>
  )
}

// SecretsPanel manages a loop's secret env vars: names are listed, values are
// write-only — entered once, stored, never shown again.
function SecretsPanel({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const { data: secrets } = useQuery({
    queryKey: ['secrets', loop.name],
    queryFn: () => api.loopSecrets(loop.name),
  })
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const invalidate = () => qc.invalidateQueries({ queryKey: ['secrets', loop.name] })

  const add = async () => {
    setBusy(true)
    setError('')
    try {
      await api.setLoopSecret(loop.name, name.trim(), value)
      setName('')
      setValue('')
      invalidate()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const remove = async (key: string) => {
    setError('')
    try {
      await api.deleteLoopSecret(loop.name, key)
      invalidate()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="side-panel">
      <h3>Secrets</h3>
      <div style={{ fontSize: '0.71875rem', color: 'var(--text-muted)', marginBottom: 8 }}>
        Env vars injected into every workstation exec (a gh token, API keys). Values are write-only — stored,
        never shown again. Applied from the next wake.
      </div>
      {(secrets ?? []).length === 0 ? (
        <div style={{ fontSize: '0.8125rem', color: 'var(--text-muted)' }}>No secrets set.</div>
      ) : (
        (secrets ?? []).map((s) => (
          <div className="row" key={s.name} style={{ alignItems: 'center' }}>
            <span className="k" style={{ fontFamily: 'var(--mono)' }}>
              {s.name}
            </span>
            <button
              className="btn danger"
              onClick={() => remove(s.name)}
              style={{ padding: '2px 8px' }}
              title="Remove secret"
            >
              ✕
            </button>
          </div>
        ))
      )}
      <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 6 }}>
        <input
          placeholder="NAME"
          value={name}
          onChange={(e) => setName(e.target.value)}
          style={{ fontFamily: 'var(--mono)' }}
        />
        <input type="password" placeholder="value" value={value} onChange={(e) => setValue(e.target.value)} />
        {error && <div className="form-error">{error}</div>}
        <button className="btn primary" onClick={add} disabled={busy || !name.trim() || !value}>
          {busy ? 'Saving…' : 'Add secret'}
        </button>
      </div>
    </div>
  )
}

// Who the loop may message privately, and whether it can yet. The readiness
// line says what is missing rather than reporting a boolean: an operator who
// reads "not ready" has no way to guess that the fix is for a person to send
// the bot a message, and the refusal itself only ever happens inside a turn.
function OwnerPanel({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const { data: senders } = useQuery({ queryKey: ['senders'], queryFn: api.senders })
  const [error, setError] = useState('')

  // Only an allowed sender can be made owner, so only they are offered. The
  // current owner joins the list even if they have since been blocked or
  // removed — a picker that silently drops the value it is showing would
  // read as "no owner".
  const options = useMemo(() => {
    const allowed = (senders ?? []).filter((s) => s.status === 'allowed')
    const id = loop.owner_tg_user_id
    if (id && !allowed.some((s) => s.tg_user_id === id)) {
      return [...allowed, { tg_user_id: id, username: loop.owner_username ?? '', display: '' }]
    }
    return allowed
  }, [senders, loop.owner_tg_user_id, loop.owner_username])

  const setOwner = useMutation({
    mutationFn: (tgUserID: number) => api.setOwner(loop.name, tgUserID),
    onSuccess: (updated) => {
      // The handler returns the updated view, and changing the owner drops
      // the captured chat: seeding the cache shows that fall back to waiting
      // straight away instead of after a refetch.
      qc.setQueryData(['loop', loop.name], updated)
      setError('')
    },
    onError: (e) => setError(e instanceof Error ? e.message : String(e)),
  })

  return (
    <div className="field" style={{ marginTop: 12 }}>
      <label htmlFor="owner-select">owner</label>
      <select
        id="owner-select"
        value={loop.owner_tg_user_id ?? ''}
        disabled={setOwner.isPending}
        onChange={(e) => setOwner.mutate(Number(e.target.value))}
      >
        {!loop.owner_tg_user_id && <option value="">No owner set</option>}
        {options.map((s) => (
          <option key={s.tg_user_id} value={s.tg_user_id}>
            {senderLabel(s)}
          </option>
        ))}
      </select>
      {error && (
        <div className="form-error" style={{ marginTop: 6 }}>
          {error}
        </div>
      )}
      <div className={`hint ${loop.owner_dm_ready ? 'ok' : ''}`}>{readiness(loop)}</div>
    </div>
  )
}

// An owner is picked by a person, so lead with the handle and keep the display
// name behind it. The bare id is the last resort for a sender with neither.
function senderLabel(s: Pick<TGSender, 'tg_user_id' | 'username' | 'display'>): string {
  if (!s.username) return s.display || String(s.tg_user_id)
  return s.display ? `@${s.username} — ${s.display}` : `@${s.username}`
}

// What the operator can do about it, in words. Never "ready: false".
function readiness(loop: LoopView): string {
  if (!loop.owner_tg_user_id) return 'No owner set — this loop cannot message anyone privately.'
  const owner = loop.owner_username ? `@${loop.owner_username}` : `sender ${loop.owner_tg_user_id}`
  if (loop.owner_dm_ready) return `Ready — the loop can message ${owner} privately.`
  const bot = loop.tg_bot_username ? `@${loop.tg_bot_username}` : "this loop's bot"
  return `Waiting for ${owner} to message ${bot}. A bot cannot open a private chat, so there is nowhere to send until they write there first.`
}

// ControlRoomThread renders the loop's private control_room conversation:
// the operator's composer messages and the loop's control_room sends,
// oldest first. Status notes stay on the timeline pane with their turns.
function ControlRoomThread({ msgs }: { msgs: ChatMessage[] }) {
  if (msgs.length === 0) {
    return <div className="empty">A private thread between you and this loop. Nothing yet.</div>
  }
  return (
    <div className="timeline">
      {[...msgs].reverse().map((m) => (
        <div key={m.id} className={`knot${m.origin === 'loop' ? '' : ' inbound'}`}>
          <div className="who">
            <span className="author">@{m.author}</span> ·{' '}
            {new Date(m.ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </div>
          <div className={m.origin === 'loop' ? 'plain' : 'bubble'}>{m.text}</div>
        </div>
      ))}
    </div>
  )
}

export default function LoopDetail() {
  const { name = '' } = useParams()
  const nav = useNavigate()
  const qc = useQueryClient()
  const [liveText, setLiveText] = useState('')
  // A power control the server reports as under way, from the workstation
  // frames — the only progress signal a long verb has.
  const [runningVerb, setRunningVerb] = useState('')
  const [draft, setDraft] = useState('')
  const [dest, setDest] = useState<MessageDestination>('control_room')
  const [pane, setPane] = useState<'timeline' | 'control_room'>('timeline')
  const bottomRef = useRef<HTMLDivElement>(null)

  const { data: loop } = useQuery({
    queryKey: ['loop', name],
    queryFn: () => api.loop(name),
    refetchInterval: 10000,
  })
  const { data: events } = useQuery({ queryKey: ['events', name], queryFn: () => api.events(name) })
  const { data: thread } = useQuery({
    queryKey: ['conversation', name],
    queryFn: () => api.conversation(name),
    enabled: pane === 'control_room',
    refetchInterval: 5000,
  })
  const { data: turns } = useQuery({ queryKey: ['turns', name], queryFn: () => api.turns(name, 10) })
  // the rotation thresholds the context gauge's colours mean something against
  const { data: thresholds } = useQuery({ queryKey: ['settings'], queryFn: api.settings })

  useStream(
    `/api/loops/${name}/stream`,
    (item) => {
      if (item.kind === 'agent_event') {
        const p = item.payload
        if (p?.type === 'stream_event') {
          const d = extractDelta(p)
          if (d) setLiveText((t) => t + d)
          return
        }
        if (p?.type === 'result') setLiveText('')
        qc.invalidateQueries({ queryKey: ['events', name] })
        if (p?.type === 'result') {
          qc.invalidateQueries({ queryKey: ['turns', name] })
          qc.invalidateQueries({ queryKey: ['loop', name] })
        }
      } else if (item.kind === 'workstation') {
        const verb = item.payload?.verb
        setRunningVerb(typeof verb === 'string' ? verb : '')
        qc.invalidateQueries({ queryKey: ['loop', name] })
      } else if (item.kind === 'loop_status' || item.kind === 'schedule') {
        qc.invalidateQueries({ queryKey: ['loop', name] })
      }
    },
    () => {
      // A power control's terminal frame may have been sent while we were
      // disconnected — or never sent at all, if the orchestrator died mid-verb.
      // A fresh connection is the moment to stop claiming one is running.
      setRunningVerb('')
      qc.invalidateQueries({ queryKey: ['loop', name] })
    },
  )

  const entries = useMemo(() => toEntries(events ?? []), [events])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [entries.length, liveText])

  const send = async () => {
    const text = draft.trim()
    if (!text) return
    setDraft('')
    await api.message(name, text, dest)
    qc.invalidateQueries({ queryKey: ['conversation', name] })
  }

  if (!loop) return <div className="page">Loading…</div>

  const paused = loop.status === 'paused'

  return (
    <div className="page">
      <div className="detail">
        <section>
          <h1 style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10 }}>
            <SpoolGlyph spinning={loop.state === 'busy'} size={20} />
            <span style={{ fontFamily: 'var(--mono)' }}>@{loop.name}</span>
            <span className="state-name">{loop.state}</span>
            {loop.runtime === 'bare' && <span className="containment-badge">uncontained</span>}
          </h1>
          {!loop.workstation_up &&
            (loop.down_reason === 'powered_off' ? (
              <div className="ws-off-note">
                Workstation powered off — ticks are skipped and messages queue until it is powered back on.
              </div>
            ) : (
              <div className="ws-down-note">
                Workstation down{loop.workstation_detail ? `: ${loop.workstation_detail}` : ''}
              </div>
            ))}
          <div className="dest-picker" style={{ marginTop: 0, marginBottom: 12 }}>
            <button className={`dest${pane === 'timeline' ? ' on' : ''}`} onClick={() => setPane('timeline')}>
              timeline
            </button>
            <button
              className={`dest${pane === 'control_room' ? ' on' : ''}`}
              onClick={() => setPane('control_room')}
            >
              control room
            </button>
          </div>
          {pane === 'timeline' ? (
            <Timeline entries={entries} liveText={liveText} />
          ) : (
            <ControlRoomThread msgs={thread ?? []} />
          )}
          <div ref={bottomRef} />
          <div className="dest-picker">
            <span className="dest-label">to</span>
            <button
              className={`dest${dest === 'control_room' ? ' on' : ''}`}
              onClick={() => setDest('control_room')}
            >
              control room · private
            </button>
            <button className={`dest${dest === 'group' ? ' on' : ''}`} onClick={() => setDest('group')}>
              group · visible to everyone
            </button>
          </div>
          <div className="composer" style={{ marginTop: 8 }}>
            <textarea
              placeholder={
                dest === 'group'
                  ? `Post to the group as yourself — @${loop.name} is delivered either way`
                  : `Message @${loop.name} privately…`
              }
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  send()
                }
              }}
            />
            <button className="btn primary" onClick={send} disabled={!draft.trim()}>
              Send
            </button>
          </div>
        </section>

        <aside>
          <div className="side-panel">
            <h3>Next wake</h3>
            <Countdown at={loop.next_tick_at} />
            <div className="row">
              <span className="k">today</span>
              <span className="v">${loop.cost_today_usd.toFixed(2)}</span>
            </div>
            <ScheduleEditor
              name={name}
              current={{
                tick: loop.tick_interval_sec,
                min: loop.min_wake_sec,
                max: loop.max_wake_sec,
                idle: loop.idle_timeout_sec,
              }}
            />
          </div>

          <div className="side-panel">
            <h3>Controls</h3>
            <div className="controls">
              {paused ? (
                <button
                  className="btn sm"
                  onClick={() =>
                    api.resume(name).then(() => qc.invalidateQueries({ queryKey: ['loop', name] }))
                  }
                >
                  Resume
                </button>
              ) : (
                <button
                  className="btn sm"
                  onClick={() =>
                    api.pause(name).then(() => qc.invalidateQueries({ queryKey: ['loop', name] }))
                  }
                >
                  Pause
                </button>
              )}
              <button className="btn sm" onClick={() => api.wake(name)}>
                Wake now
              </button>
              <button
                className="btn sm"
                onClick={() => {
                  if (
                    confirm(
                      `Rotate @${name}'s session? It writes a handoff note and continues fresh; conversational context is shed by design.`,
                    )
                  ) {
                    api.rotate(name)
                  }
                }}
              >
                Rotate session
              </button>
              <button className="btn sm danger" onClick={() => api.kill(name)}>
                Kill process
              </button>
              <button
                className="btn sm danger"
                onClick={() => {
                  if (confirm(`Delete loop @${name}? Its worktree is removed; the branch is kept.`)) {
                    api.deleteLoop(name, true).then(() => {
                      qc.invalidateQueries({ queryKey: ['loops'] })
                      nav('/')
                    })
                  }
                }}
              >
                Delete loop
              </button>
            </div>
          </div>

          <ContextPanel loop={loop} turns={turns ?? []} thresholds={thresholds} />

          <WorkstationPanel loop={loop} runningVerb={runningVerb} />

          <ModelPanel loop={loop} />

          <div className="side-panel">
            <h3>Mission</h3>
            <div style={{ fontSize: '0.8125rem', whiteSpace: 'pre-wrap' }}>{loop.mission}</div>
          </div>

          <div className="side-panel">
            <h3>Workspace</h3>
            {loop.workspace_mode === 'none' ? (
              <div className="row">
                <span className="k">none (conversational)</span>
              </div>
            ) : (
              <>
                <div className="row">
                  <span className="k">path</span>
                  <span className="v">{loop.workspace_path}</span>
                </div>
                {loop.branch && (
                  <div className="row">
                    <span className="k">branch</span>
                    <span className="v">{loop.branch}</span>
                  </div>
                )}
              </>
            )}
          </div>

          <div className="side-panel">
            <h3>Telegram</h3>
            {loop.has_tg_token ? (
              <>
                <div className="row">
                  <span className="k">bot</span>
                  <span className="v">@{loop.tg_bot_username || '?'}</span>
                </div>
                <div className="row">
                  <span className="k">group</span>
                  <span className="v">
                    {loop.tg_group_chat_id ? 'bound' : 'waiting for a group message…'}
                  </span>
                </div>
                <OwnerPanel loop={loop} />
              </>
            ) : (
              <div style={{ fontSize: '0.8125rem', color: 'var(--text-muted)' }}>
                No bot connected. Add a token in loop settings to talk from Telegram.
              </div>
            )}
          </div>

          <SecretsPanel loop={loop} />

          <div className="side-panel">
            <h3>Recent turns</h3>
            {(turns ?? []).map((t) => (
              <div className="row" key={t.id}>
                <span className="k">
                  {new Date(t.started_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}{' '}
                  {t.trigger}
                </span>
                <span className="v">{t.ended_at ? `$${t.cost_usd.toFixed(3)}` : '…'}</span>
              </div>
            ))}
          </div>
        </aside>
      </div>
    </div>
  )
}
