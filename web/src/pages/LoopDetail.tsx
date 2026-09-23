import { useState, useRef, useEffect, useLayoutEffect, useMemo, type UIEvent } from 'react'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import {
  api,
  ChatMessage,
  EVENT_WINDOW,
  LoopEvent,
  LoopView,
  MessageDestination,
  Settings,
  TGSender,
  Turn,
} from '../api'
import { formatTokens, fillTone, hasFillPct, formatUsd } from '../format'
import { tokenSubmittable } from '../forms'
import { missionDraft, missionSaveResult, missionSaveWarning } from '../mission'
import { MODEL_OPTIONS, EFFORT_OPTIONS, PACING_OPTIONS } from '../options'
import { useStream } from '../stream'
import { toEntries, extractDelta } from '../timeline'
import { MessageKnot } from '../components/MessageKnot'
import { UndeliveredPane } from '../components/UndeliveredPane'
import { Timeline } from '../components/Timeline'
import { SpoolGlyph } from '../components/Spool'
import { EditIcon } from '../components/Icons'

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
        <input className="tick-input" value={tick} onChange={(e) => setTick(e.target.value)} />
        <span className="k">min</span>
        <button className="btn sm" onClick={() => mut.mutate()} disabled={mut.isPending}>
          Set
        </button>
      </span>
    </div>
  )
}

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
      <select className="panel-select" value={loop.model} onChange={(e) => patch({ model: e.target.value })}>
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
      <select
        className="panel-select"
        value={loop.effort}
        onChange={(e) => patch({ effort: e.target.value })}
      >
        {EFFORT_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <div className="row" style={{ marginTop: 8 }}>
        <span className="k">pacing</span>
      </div>
      <select
        className="panel-select"
        value={loop.pacing}
        onChange={(e) => patch({ pacing: e.target.value })}
      >
        {PACING_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <div className="panel-note">Changes apply from the next wake.</div>
    </div>
  )
}

// Context occupancy the turn measured at its last API call — what the next
// prompt would carry into the window. Turns recorded before the measure
// existed read 0: unmeasured, not empty.
function contextTokens(turn: Turn): number {
  return turn.context_tokens
}

// MissionPanel shows the mission clamped to five lines, because a dogfood
// mission runs to paragraphs and rendering all of them made the page 3,815px
// tall and left the four panels below this one past 4,100px (#131). It is the
// side column's length a long mission costs, not the timeline's position —
// the timeline is the other column and never moved. Five lines because
// missions separate paragraphs with a blank line, and at four that gap took
// the last slot and left an ellipsis alone on a row.
//
// The clamp is CSS, so the whole text stays in the DOM and stays selectable;
// the toggle only appears when there is something hidden, which is a
// measurement rather than a guess about length.
function MissionPanel({
  name,
  mission,
  hasSession,
}: {
  name: string
  mission: string
  // Whether the loop has a session to rotate, which decides what saving
  // costs it. Read from the loop the page already has rather than asked for:
  // it is the same row the panel draws the mission from.
  hasSession: boolean
}) {
  const qc = useQueryClient()
  const body = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(() => readMissionChoice(name))
  const [clamped, setClamped] = useState(false)
  const [draft, setDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState('')

  // Whether the clamp is actually hiding anything. Re-measured when the text
  // or the width changes: the same mission clamps at 1440px and may not at
  // 320px, where the column is wider in lines.
  useEffect(() => {
    const el = body.current
    if (!el) return
    const measure = () => setClamped(el.scrollHeight > el.clientHeight + 1)
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [mission, open])

  const toggle = () => {
    const next = !open
    setOpen(next)
    writeMissionChoice(name, next)
  }

  const state = missionDraft(mission, draft ?? '')

  const save = async () => {
    setBusy(true)
    setError('')
    try {
      // Only the mission: a PATCH naming one field leaves the rest of the
      // loop as it stands, including whatever the Telegram poller learned
      // while this panel was open.
      const saved = await api.patchLoop(name, { mission: (draft ?? '').trim() })
      // Both, because the Fleet row draws the mission too and a stale one
      // there is the operator's evidence that the save did not take.
      qc.invalidateQueries({ queryKey: ['loop', name] })
      qc.invalidateQueries({ queryKey: ['loops'] })
      setDraft(null)
      // The editor closes on a save, so this is the only place the operator
      // learns what became of the session they were warned about.
      setResult(missionSaveResult(name, saved.rotation))
    } catch (e) {
      // The server's own words: it is the one that refuses an empty mission
      // or a loop that has since been archived, and it says which.
      setError(e instanceof Error ? e.message : 'could not save the mission')
    } finally {
      setBusy(false)
    }
  }

  if (draft !== null) {
    return (
      <div className="side-panel">
        {/* The same head row as the read view, minus its action: the pen
            opened this, and a panel whose heading jumps when it does is one
            the operator has to re-find. */}
        <div className="panel-head">
          <h3>Mission</h3>
        </div>
        <textarea
          className="mission-edit"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={10}
          autoFocus
        />
        {/* Before Save, not after: a rotation costs the loop its context,
            and an operator who learns that from the result has already
            paid it. */}
        <div className="mission-warning">{missionSaveWarning(name, hasSession)}</div>
        {state.reason && <div className="form-error">{state.reason}</div>}
        {error && <div className="form-error">{error}</div>}
        <div className="controls" style={{ marginTop: 8 }}>
          <button className="btn primary sm" onClick={save} disabled={busy || !state.canSave}>
            {busy ? 'Saving…' : 'Save'}
          </button>
          <button
            className="btn sm"
            onClick={() => {
              setDraft(null)
              setError('')
            }}
            disabled={busy}
          >
            Cancel
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="side-panel">
      {/* The action sits on the heading rather than under the text, where it
          stood beside the clamp toggle: two underlined words in a row, one
          changing how much of the mission is shown and one changing the
          mission, reading as a pair of the same kind of thing. A pen at the
          panel's corner is the control an operator looks for, and it cannot
          be mistaken for the toggle it no longer sits next to. */}
      <div className="panel-head">
        <h3>Mission</h3>
        <button
          className="panel-action"
          onClick={() => {
            setDraft(mission)
            setError('')
            // Last save's outcome, not this one's.
            setResult('')
          }}
          // Icon-only, so the name is the label: a button whose accessible
          // name is empty is one a screen reader announces as "button".
          aria-label="Edit the mission"
          title="Edit the mission"
        >
          <EditIcon />
        </button>
      </div>
      <div ref={body} className={`panel-body${open ? '' : ' clamped'}`}>
        {mission}
      </div>
      {result && <div className="mission-result">{result}</div>}
      {(clamped || open) && (
        <div className="mission-controls">
          <button className="thinking-toggle mission-toggle" onClick={toggle}>
            {open ? 'show less' : 'show the whole mission'}
          </button>
        </div>
      )}
    </div>
  )
}

// The reader's choice, for as long as the tab lives — and the control room's
// first Web Storage, so it is wrapped rather than trusted. `sessionStorage`
// throws on access, not on failure, in a browser configured to refuse it, and
// this read runs in a state initialiser: unwrapped, that takes the whole loop
// page down. A remembered toggle is not worth a blank screen.
function readMissionChoice(name: string): boolean {
  try {
    return sessionStorage.getItem(missionKey(name)) === 'open'
  } catch {
    return false
  }
}

function writeMissionChoice(name: string, open: boolean): void {
  try {
    sessionStorage.setItem(missionKey(name), open ? 'open' : 'closed')
  } catch {
    /* the panel still opens; only the memory of the choice is lost */
  }
}

function missionKey(name: string): string {
  return `spool.mission.${name}`
}

// ContextFill is the occupancy of the model's own window — the glanceable
// number. Only rendered when Spool knows the window and the server reported a
// fill; an unknown limit gets absolute tokens rather than a percentage against
// a guess, and an unreported fill would draw a bar of `width: undefined%`.
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
      {loop.context_limit_tokens > 0 && hasFillPct(loop.context_fill_pct) && (
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
      <div className="panel-note">
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
        <div className="panel-note">
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
      <div className="panel-note leading">
        Env vars injected into every workstation exec (a gh token, API keys). Values are write-only — stored,
        never shown again. Applied from the next wake.
      </div>
      {(secrets ?? []).length === 0 ? (
        <div className="panel-empty">No secrets set.</div>
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
        <input
          type="password"
          autoComplete="off"
          placeholder="value"
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
        {error && <div className="form-error">{error}</div>}
        <button className="btn primary" onClick={add} disabled={busy || !name.trim() || !value}>
          {busy ? 'Saving…' : 'Add secret'}
        </button>
      </div>
    </div>
  )
}

// Binding a loop to a Telegram bot, and rebinding it to another. The token is
// write-only in the same sense as a secret: it is typed, sent, and never
// rendered back — the panel above says which bot answers, which is the part
// an operator needs to recognise.
//
// It lives on the loop page because that is where a rotation is noticed. The
// old copy sent the operator to "loop settings" for a control that was never
// built, so the only way to change a token was the API; the 2026-09-18
// rotation was three curl calls (#157).
function BotTokenForm({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const close = () => {
    setOpen(false)
    setToken('')
    setError('')
  }

  const save = async () => {
    setBusy(true)
    setError('')
    try {
      await api.patchLoop(loop.name, { tg_bot_token: token.trim() })
      // The loop, not just this panel: the bound username is the server's
      // answer to whether the token worked, and the header's surface line
      // reads from the same record.
      qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      qc.invalidateQueries({ queryKey: ['loops'] })
      close()
    } catch (e) {
      // The server's own words. It validates against Telegram before storing,
      // so "telegram token rejected: …" distinguishes a typo from a revoked
      // token — and it cannot quote the token back, which is redacted at the
      // client (#146).
      setError(e instanceof Error ? e.message : 'could not save the token')
    } finally {
      setBusy(false)
    }
  }

  if (!open) {
    return (
      <button className="btn sm" style={{ marginTop: 10 }} onClick={() => setOpen(true)}>
        {loop.has_tg_token ? 'Replace token' : 'Connect a bot'}
      </button>
    )
  }
  return (
    <div className="field" style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 6 }}>
      <input
        type="password"
        placeholder="bot token from @BotFather"
        value={token}
        onChange={(e) => setToken(e.target.value)}
        // so a browser does not offer to remember a bot token. Not a
        // guarantee — browsers honour this unevenly on password fields — but
        // this input is in no form, which is the other half of not being
        // treated as a login.
        autoComplete="off"
        autoFocus
      />
      {error && <div className="form-error">{error}</div>}
      <div style={{ display: 'flex', gap: 6 }}>
        {/* `tokenSubmittable` is load-bearing, not tidiness — see its comment:
            PATCH reads an empty `tg_bot_token` as *disconnect*. */}
        <button className="btn primary" onClick={save} disabled={busy || !tokenSubmittable(token)}>
          {busy ? 'Checking…' : 'Save'}
        </button>
        <button className="btn" onClick={close} disabled={busy}>
          Cancel
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
        <MessageKnot key={m.id} msg={m} />
      ))}
    </div>
  )
}

type Pane = 'timeline' | 'control_room' | 'undelivered'

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
  // The Fleet badge opens this page on its Undelivered pane (`?pane=`, #281);
  // read once, as where the page starts, so switching panes afterwards is not
  // a navigation the back button has to walk through.
  const [searchParams] = useSearchParams()
  const [pane, setPane] = useState<Pane>(() =>
    searchParams.get('pane') === 'undelivered' ? 'undelivered' : 'timeline',
  )
  const paneRef = useRef<HTMLDivElement>(null)
  // Whether the pane should follow new entries. A reader who has scrolled up
  // is reading; yanking them back to the tail because a turn arrived loses
  // their place. Near the bottom counts as at the bottom — a couple of lines
  // of slack, so the follow survives the rounding a fresh entry introduces.
  const following = useRef(true)
  // Which entry the reader is parked on, and where in the pane it sits, for
  // the moment the window slides under them.
  const anchor = useRef<{ id: string; offset: number } | null>(null)
  // The panes render into the same scroll container, so a position recorded
  // in one means nothing in another: the ids come from different tables and
  // would collide by coincidence. Switching panes starts at the tail, which is
  // where a reader opening a conversation wants to be anyway.
  const showPane = (next: Pane) => {
    following.current = true
    anchor.current = null
    setPane(next)
  }

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

  // History the reader has walked back to, and everything the live window has
  // shown since they started walking. Empty until they ask: before the first
  // click the page renders exactly the window #119 gives it, so an untouched
  // timeline behaves as it always has.
  //
  // It has to keep accumulating, not merely start from a seed. The live query
  // is not a page — it is `id < ? ORDER BY id DESC LIMIT n`, recomputed on
  // every refetch, so it is the newest N events and it *slides*: each event
  // the loop emits pushes one off its old end. An event born after the click
  // therefore lives only in that window, and one window later it has slid out
  // of it — belonging to neither side, a hole in the middle of the transcript
  // rather than a trim at its edge. So every window this page is handed is
  // written into the record, not merged for display and dropped.
  //
  // A ref, because this is a cache of what has been seen rather than state
  // the render reads and re-reads: writing it back through `setState` from an
  // effect is a render cascade (`react-hooks/set-state-in-effect`), and the
  // merge is idempotent, so doing it as the entries are derived is safe under
  // a double render. `walked` counts the pages fetched, purely to ask for the
  // render that follows one.
  //
  // Tagged with the loop it belongs to rather than cleared when the route
  // changes: ids come from one table, so one loop's history merged into
  // another's window would interleave two conversations silently. Reading the
  // tag makes the stale case impossible; clearing it in an effect would still
  // render the wrong timeline once, before the effect ran.
  const [record, setRecord] = useState<{
    loop: string
    events: Map<number, LoopEvent>
    upTo: number
    // The stretch the window jumped over, if it ever did: events after `from`
    // and before `to` that the page was never handed and only the store has.
    // One jump is tracked, not a list: a second jump while the first is
    // still being walked replaces it, and the earlier stretch stays missing.
    // Chosen rather than overlooked — it takes two spells of the page going
    // more than a window without a refetch, one inside the other's walk, and
    // the cost of being wrong is history a reader can reload to recover.
    hole: { from: number; to: number } | null
  } | null>(null)
  const history = record?.loop === name ? record : null
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [olderFailed, setOlderFailed] = useState(false)
  // Set when a page comes back shorter than it asked for: the store had
  // nothing more to give, so this is the loop's first event.
  const [atFirst, setAtFirst] = useState('')
  const atFirstEvent = atFirst === name

  // Writing the live window into the record as it arrives, during render. The
  // two obvious places are both closed: an effect that calls setState is a
  // render cascade (`react-hooks/set-state-in-effect`), and a ref mutated
  // while deriving is a ref read during render (`react-hooks/refs`). This is
  // React's own answer for state that has to track something it is handed —
  // the component re-runs before anything is committed, so no extra paint.
  // `upTo` is what makes it terminate: it only fires when the window's newest
  // id has actually moved past what has already been folded in.
  const newest = events?.length ? events[events.length - 1].id : 0
  if (history && newest > history.upTo) {
    const merged = new Map(history.events)
    for (const e of events ?? []) merged.set(e.id, e)
    // Noted here, before `upTo` moves past it: if the window's oldest event
    // is beyond where the record ends, the events in between were never
    // delivered to this page at all, and folding the window in would hide
    // that by making the record's newest id look continuous.
    const jumped = (events?.[0]?.id ?? 0) > history.upTo + 1
    setRecord({
      loop: name,
      events: merged,
      upTo: newest,
      hole: jumped ? { from: history.upTo, to: events![0].id } : history.hole,
    })
  }

  // Accumulating only keeps what the page was handed, and it is not handed
  // everything: if more than a window's worth of events lands between two
  // refetches — a tab left open overnight, a long disconnect — the window
  // jumps clean over the events in between and they reach nobody. Nothing
  // held client-side can close that; only the store still has them. On a loop
  // being watched this never fires, because the stream refetches once per
  // event and the window advances one at a time.
  const hole = history?.hole
  const filling = useRef(false)
  useEffect(() => {
    if (!hole || filling.current) return
    filling.current = true
    // A page at a time, walking forward. Not `hole.to - hole.from` as a count:
    // ids are one AUTOINCREMENT shared by every loop, so a loop's own ids are
    // sparse in it — measured across this fleet's four loops, between 8.9% and
    // 32.8% dense — and the span over-counts the missing rows several times
    // over. `ListByLoop` has no far bound and the endpoint does not clamp
    // `limit`, so an over-counted span is not a harmless over-ask: it is
    // "every event this loop has after `from`", payloads and all.
    api
      .eventsAfter(name, hole.from, EVENT_WINDOW)
      .then((page) => {
        // No in-flight cancellation: a page that has already been fetched is
        // always landed. Dropping it on cleanup would strand the walk —
        // nothing re-fires the effect, because `hole` only moves when a page
        // lands, and a quiet loop's refetch returns a structurally identical
        // array, so `events` does not change either. Landing it is also what
        // steps the walk forward. `setRecord` rejects a stale loop, which is
        // the case that matters, and a post-unmount setState is a no-op.
        const inside = page.filter((e) => e.id < hole.to)
        setRecord((prev) => {
          if (prev?.loop !== name) return prev
          const merged = new Map(prev.events)
          for (const e of inside) merged.set(e.id, e)
          // Done when the page ran past the far side of the hole, or the
          // store had less than a page left to give. Otherwise the walk
          // continues from where this page ended.
          const done = inside.length < page.length || page.length < EVENT_WINDOW
          const last = inside.length ? inside[inside.length - 1].id : hole.from
          return { ...prev, events: merged, hole: done ? null : { from: last, to: hole.to } }
        })
      })
      .catch(() => {
        // Left standing rather than reported: this is a background repair the
        // reader did not ask for, and the button's notice belongs to the
        // button. The next window to arrive retries it — and if none does,
        // because the loop has gone quiet, the stretch stays missing until
        // the page is reloaded. Both halves of that have to be true at once:
        // a jump, then a failed page, then silence.
      })
      .finally(() => {
        filling.current = false
      })
    // `events` is the retry for a *failed* page, and the weaker of the two:
    // it needs a window to change, which a quiet loop never does. A page that
    // succeeds needs no help — landing it moves `hole`, which re-runs this.
  }, [hole, name, events])

  const entries = useMemo(() => {
    if (!history) return toEntries(events ?? [])
    // The hole travels with the events: a turn's cost is read from the turns
    // before it, and the stretch the window jumped over is exactly where that
    // evidence is missing.
    return toEntries(
      [...history.events.values()].sort((a, b) => a.id - b.id),
      history.hole,
    )
  }, [events, history])

  // The oldest event held, which is not the oldest entry rendered: `toEntries`
  // drops the events the room has no copy for (proc_exit, an assistant line
  // with no blocks), so a cursor taken from the first entry would ask again
  // for the events sitting above it and never advance.
  const oldestLoaded = () => {
    let oldest = Infinity
    for (const id of history ? history.events.keys() : (events ?? []).map((e) => e.id)) {
      if (id < oldest) oldest = id
    }
    return oldest
  }

  const loadOlder = async () => {
    const oldest = oldestLoaded()
    if (!isFinite(oldest) || loadingOlder) return
    setLoadingOlder(true)
    setOlderFailed(false)
    // A reader who scrolled up to reach this control already has an anchor,
    // and it is the right one — leave it alone. The case that needs help is a
    // timeline short enough that the control is visible from the tail: that
    // reader is still "following", so the effect would hold them at the
    // bottom and three hundred entries would arrive without the pane
    // appearing to move at all. Park them on the entry that is about to stop
    // being the oldest, which is where the new history meets the old.
    if (following.current) {
      following.current = false
      anchor.current = { id: String(entries[0]?.id ?? ''), offset: 0 }
    }
    try {
      const page = await api.eventsBefore(name, oldest)
      if (page.length < EVENT_WINDOW) setAtFirst(name)
      const merged = new Map(history?.events ?? [])
      for (const e of page) merged.set(e.id, e)
      for (const e of events ?? []) merged.set(e.id, e)
      setRecord({ loop: name, events: merged, upTo: newest, hole: history?.hole ?? null })
    } catch {
      // A failure only the console knows about is a failure nobody knows
      // about (#147): the button would re-enable and the history would not
      // move, which reads exactly like reaching the first event.
      setOlderFailed(true)
    } finally {
      setLoadingOlder(false)
    }
  }

  useLayoutEffect(() => {
    const el = paneRef.current
    // A list rather than a conversation: it reads from the top, and nothing
    // arrives at its tail to follow.
    if (!el || pane === 'undelivered') return
    if (following.current) {
      el.scrollTop = el.scrollHeight
      return
    }
    // Not following: hold the reader's place against a window that slides.
    // Each new event pushes the oldest one out of the window, so everything
    // above shifts up; keeping `scrollTop` would quietly walk the reader
    // down their own history. The anchor is the entry they were looking at.
    const held = anchor.current
    if (!held) return
    const parked = el.querySelector(`[data-entry-id="${held.id}"]`)
    if (parked instanceof HTMLElement) el.scrollTop = parked.offsetTop - held.offset
    // `entries`, not its length: the window is a fixed size, so a new event
    // slides it rather than growing it, and a length dependency would miss
    // exactly the update this effect exists for.
  }, [entries, liveText, pane, thread])

  // Where the reader is, recorded as they move: whether they are at the tail,
  // and if not, the first entry still on screen and how far down the pane it
  // sits. The effect above restores that when the window slides.
  const readingPosition = (e: UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget
    following.current = el.scrollHeight - el.scrollTop - el.clientHeight < 32
    anchor.current = null
    if (following.current) return
    for (const knot of el.querySelectorAll<HTMLElement>('[data-entry-id]')) {
      if (knot.offsetTop + knot.offsetHeight <= el.scrollTop) continue
      anchor.current = { id: knot.dataset.entryId ?? '', offset: knot.offsetTop - el.scrollTop }
      break
    }
  }

  const send = async () => {
    const text = draft.trim()
    if (!text) return
    setDraft('')
    await api.message(name, text, dest)
    qc.invalidateQueries({ queryKey: ['conversation', name] })
  }

  if (!loop) return <div className="page measure placeholder">Loading…</div>

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
            <button
              className={`dest${pane === 'timeline' ? ' on' : ''}`}
              onClick={() => showPane('timeline')}
            >
              timeline
            </button>
            <button
              className={`dest${pane === 'control_room' ? ' on' : ''}`}
              onClick={() => showPane('control_room')}
            >
              control room
            </button>
            {/* Only while there is something in it, like the nav entry it
                replaces (#263): a permanent tab reading 0 on a healthy loop
                is one the operator learns to skip. Kept while it is open, so
                dismissing the last row does not pull the pane out from under
                the click. The count is the one the Fleet badge shows. */}
            {(loop.undelivered > 0 || pane === 'undelivered') && (
              <button
                className={`dest bad${pane === 'undelivered' ? ' on' : ''}`}
                onClick={() => showPane('undelivered')}
              >
                undelivered{loop.undelivered > 0 && ` · ${loop.undelivered}`}
              </button>
            )}
          </div>
          {/* The history scrolls inside the page rather than growing it, so the
              composer below stays where the operator left it however long the
              loop has been running. */}
          <div className="pane-scroll" ref={paneRef} onScroll={readingPosition}>
            {pane === 'timeline' ? (
              <Timeline
                entries={entries}
                liveText={liveText}
                onLoadOlder={atFirstEvent ? undefined : loadOlder}
                loadingOlder={loadingOlder}
                olderFailed={olderFailed}
              />
            ) : pane === 'control_room' ? (
              <ControlRoomThread msgs={thread ?? []} />
            ) : (
              <UndeliveredPane name={loop.name} />
            )}
          </div>
          <div className="dest-picker">
            <span className="dest-label">to</span>
            <button
              className={`dest${dest === 'control_room' ? ' on' : ''}`}
              onClick={() => setDest('control_room')}
            >
              control room · private
            </button>
            <button className={`dest${dest === 'group' ? ' on' : ''}`} onClick={() => setDest('group')}>
              group · the loops, not Telegram
            </button>
          </div>
          <div className="composer" style={{ marginTop: 8 }}>
            <textarea
              placeholder={
                dest === 'group'
                  ? `Post to the group — the loops see it, nothing goes to Telegram; @${loop.name} is delivered either way`
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
              <span className="v" title={loop.cost_day ? `spend on ${loop.cost_day}` : undefined}>
                {formatUsd(loop.cost_today_usd)}
              </span>
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

          <MissionPanel name={name} mission={loop.mission} hasSession={loop.current_session_id !== ''} />

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
                <BotTokenForm loop={loop} />
                <OwnerPanel loop={loop} />
              </>
            ) : (
              <>
                <div className="panel-empty">No bot connected — the loop cannot reach Telegram.</div>
                <BotTokenForm loop={loop} />
              </>
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
