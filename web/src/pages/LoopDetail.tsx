import { useState, useRef, useEffect, useMemo } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { api, LoopView } from '../api'
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
            background: 'var(--ink)',
            border: '1px solid var(--ink-line)',
            borderRadius: 5,
            color: 'var(--linen)',
            padding: '2px 6px',
            fontFamily: 'var(--mono)',
            fontSize: 12,
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
  background: 'var(--ink)',
  border: '1px solid var(--ink-line)',
  borderRadius: 5,
  color: 'var(--linen)',
  padding: '4px 8px',
  fontSize: 12.5,
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
      <div style={{ fontSize: 11.5, color: 'var(--muted)', marginTop: 8 }}>
        Changes apply from the next wake.
      </div>
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
      <div style={{ fontSize: 11.5, color: 'var(--muted)', marginBottom: 8 }}>
        Env vars injected into every workstation exec (a gh token, API keys). Values are write-only — stored,
        never shown again. Applied from the next wake.
      </div>
      {(secrets ?? []).length === 0 ? (
        <div style={{ fontSize: 13, color: 'var(--muted)' }}>No secrets set.</div>
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

export default function LoopDetail() {
  const { name = '' } = useParams()
  const nav = useNavigate()
  const qc = useQueryClient()
  const [liveText, setLiveText] = useState('')
  const [draft, setDraft] = useState('')
  const bottomRef = useRef<HTMLDivElement>(null)

  const { data: loop } = useQuery({
    queryKey: ['loop', name],
    queryFn: () => api.loop(name),
    refetchInterval: 10000,
  })
  const { data: events } = useQuery({ queryKey: ['events', name], queryFn: () => api.events(name) })
  const { data: turns } = useQuery({ queryKey: ['turns', name], queryFn: () => api.turns(name, 10) })

  useStream(`/api/loops/${name}/stream`, (item) => {
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
    } else if (item.kind === 'loop_status' || item.kind === 'schedule') {
      qc.invalidateQueries({ queryKey: ['loop', name] })
    }
  })

  const entries = useMemo(() => toEntries(events ?? []), [events])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [entries.length, liveText])

  const send = async () => {
    const text = draft.trim()
    if (!text) return
    setDraft('')
    await api.message(name, text)
  }

  if (!loop) return <div className="page">Loading…</div>

  const paused = loop.status === 'paused'

  return (
    <div className="page">
      <div className="detail">
        <section>
          <h1 style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <SpoolGlyph spinning={loop.state === 'busy'} size={20} />
            <span style={{ fontFamily: 'var(--mono)' }}>@{loop.name}</span>
            <span className="state-name">{loop.state}</span>
            {loop.runtime === 'bare' && <span className="containment-badge">uncontained</span>}
          </h1>
          {!loop.workstation_up && (
            <div className="ws-down-note">
              Workstation down{loop.workstation_detail ? `: ${loop.workstation_detail}` : ''}
            </div>
          )}
          <Timeline entries={entries} liveText={liveText} />
          <div ref={bottomRef} />
          <div className="composer">
            <textarea
              placeholder={`Message @${loop.name}…`}
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

          <ModelPanel loop={loop} />

          <div className="side-panel">
            <h3>Mission</h3>
            <div style={{ fontSize: 13, whiteSpace: 'pre-wrap' }}>{loop.mission}</div>
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
              </>
            ) : (
              <div style={{ fontSize: 13, color: 'var(--muted)' }}>
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
