import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import { EFFORT_OPTIONS, PACING_OPTIONS, RUNTIME_OPTIONS, runtimeNote } from '../options'
import { useModelOptions } from '../models'
import { useHubRuntime } from '../version'
import { customModelError, startsInFleetChannel } from '../forms'

export default function NewLoop() {
  const nav = useNavigate()
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [mission, setMission] = useState('')
  const [model, setModel] = useState('')
  const [customModel, setCustomModel] = useState('')
  const modelOptions = useModelOptions()
  const [effort, setEffort] = useState('')
  const [pacing, setPacing] = useState<'fixed' | 'self'>('fixed')
  const [runtime, setRuntime] = useState('')
  const [wsPath, setWsPath] = useState('')
  const [wsInfo, setWsInfo] = useState<{ exists: boolean; is_git: boolean } | null>(null)
  const [interval, setIntervalMin] = useState('30')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // What the hub was started as, and whether it will accept an uncontained
  // loop at all. Both are constants under a running server; the form reads
  // them rather than assuming docker, because on a single-machine install the
  // default is bare and the warning belongs on every creation (#255).
  const { data: health } = useHubRuntime()
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  // `null` until the operator touches the switch, so the default follows the
  // fleet list if it loads after the form. Until that list answers there is
  // no default to show: the switch waits, and a create sent meanwhile says
  // nothing, leaving the choice to the server, which reads the same rule.
  const { data: fleet } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const [inChannel, setInChannel] = useState<boolean | null>(null)
  const joinsChannel = inChannel ?? (fleet ? startsInFleetChannel(fleet) : undefined)
  // Until both have answered the form knows nothing about this hub, and a
  // guess is worse than the silence it replaces: a create that names `docker`
  // on a bare-only hub is refused, where one that names nothing was always
  // right. So an unanswered hub sends no runtime and says it is still asking
  // — and it stays that way if the request failed, rather than settling into
  // a wrong answer that submits.
  const hubKnown = health !== undefined && settings !== undefined
  const bareAllowed = settings?.bare_allowed === true
  const chosenRuntime = runtime || (hubKnown ? (health?.runtime ?? 'docker') : '')
  const note = chosenRuntime
    ? runtimeNote(chosenRuntime)
    : { text: 'Asking this hub what it runs loops as…', warn: false }
  const runtimeChoices = RUNTIME_OPTIONS.filter((o) => o.value !== 'bare' || bareAllowed)

  const inspect = async (p: string) => {
    setWsPath(p)
    setWsInfo(null)
    if (!p.trim()) return
    try {
      setWsInfo(await api.inspectWorkspace(p))
    } catch {
      /* leave unset */
    }
  }

  const create = async () => {
    const chosenModel = model === '__custom__' ? customModel.trim() : model
    // Loop create refuses the same ids (#341); checked here too so the
    // sentence lands beside the field rather than after the request.
    const invalid = model === '__custom__' ? customModelError(chosenModel) : ''
    if (invalid) {
      setError(invalid)
      return
    }
    setBusy(true)
    setError('')
    try {
      const loop = await api.createLoop({
        name: name.trim(),
        mission: mission.trim(),
        model: chosenModel || undefined,
        effort: effort || undefined,
        pacing,
        // Sent explicitly, so the same click means the same thing on a
        // docker hub and a bare one — but only once the hub has said what it
        // is. Omitted otherwise, which is what the form did before this
        // choice existed: the hub resolves its own default, and that cannot
        // be wrong.
        runtime: (chosenRuntime as 'bare' | 'docker') || undefined,
        workspace_path: wsPath.trim() || undefined,
        tick_interval_sec: Math.max(60, Number(interval) * 60),
        // Sent whenever the switch shows a state, so it means what it shows; the
        // server's own default covers a form that has none yet.
        in_fleet_channel: joinsChannel,
      })
      qc.invalidateQueries({ queryKey: ['loops'] })
      nav(`/loops/${loop.name}`)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const slugOk = /^[a-z0-9][a-z0-9_-]{1,31}$/.test(name.trim().toLowerCase())

  return (
    <div className="page measure">
      <h1>New loop</h1>
      <div className="form">
        {error && (
          <div className="form-error" role="alert">
            {error}
          </div>
        )}

        <div className="field">
          <label htmlFor="nl-name">Name</label>
          <input
            id="nl-name"
            placeholder="e.g. watcher, fixer, researcher"
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
            autoFocus
          />
          <div className="hint">Lowercase, 2–32 chars. Others reach this loop as @{name || 'name'}.</div>
        </div>

        <div className="field">
          <label htmlFor="nl-mission">Mission</label>
          <textarea
            id="nl-mission"
            rows={4}
            placeholder="What should this loop keep doing? It wakes on a schedule and whenever someone messages it."
            value={mission}
            onChange={(e) => setMission(e.target.value)}
          />
        </div>

        <div className="field-row">
          <div className="field">
            <label htmlFor="nl-model">Model</label>
            <select id="nl-model" value={model} onChange={(e) => setModel(e.target.value)}>
              {modelOptions.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
              <option value="__custom__">Custom…</option>
            </select>
            {model === '__custom__' && (
              <input
                style={{ marginTop: 6 }}
                placeholder="full model id, e.g. claude-opus-5-5"
                value={customModel}
                onChange={(e) => setCustomModel(e.target.value)}
              />
            )}
          </div>
          <div className="field">
            <label htmlFor="nl-effort">Effort</label>
            <select id="nl-effort" value={effort} onChange={(e) => setEffort(e.target.value)}>
              {EFFORT_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="field-row">
          <div className="field">
            <label htmlFor="nl-pacing">Pacing</label>
            <select
              id="nl-pacing"
              value={pacing}
              onChange={(e) => setPacing(e.target.value as 'fixed' | 'self')}
            >
              {PACING_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            <div className="hint">
              {pacing === 'self'
                ? 'The loop picks its own next-wake time each turn; the interval below is only a fallback.'
                : 'Spool wakes the loop on the interval below; the loop may still adjust with a trailer.'}
            </div>
          </div>
          <div className="field">
            <label htmlFor="nl-interval">
              {pacing === 'self' ? 'Fallback interval (minutes)' : 'Tick interval (minutes)'}
            </label>
            <input id="nl-interval" value={interval} onChange={(e) => setIntervalMin(e.target.value)} />
          </div>
        </div>

        <div className="field">
          <label htmlFor="nl-runtime">Runtime</label>
          <select
            id="nl-runtime"
            value={chosenRuntime}
            onChange={(e) => setRuntime(e.target.value)}
            disabled={!hubKnown || runtimeChoices.length < 2}
          >
            {!chosenRuntime && <option value="">…</option>}
            {runtimeChoices.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <div className={`hint ${note.warn ? 'warn' : ''}`}>{note.text}</div>
        </div>

        <div className="field">
          <label htmlFor="nl-ws">Workspace (optional)</label>
          <input
            id="nl-ws"
            placeholder="/path/to/repo, or leave empty for a conversational loop"
            value={wsPath}
            onChange={(e) => inspect(e.target.value)}
          />
          {wsInfo && wsPath.trim() && (
            <div className={`hint ${wsInfo.exists ? (wsInfo.is_git ? 'ok' : '') : 'warn'}`}>
              {!wsInfo.exists
                ? 'Directory not found.'
                : wsInfo.is_git
                  ? `Git repo: an isolated worktree will be created on branch loop/${name || '<name>'}.`
                  : 'Plain directory: the loop works here directly.'}
            </div>
          )}
        </div>

        <label className="switch-row">
          <input
            type="checkbox"
            role="switch"
            checked={joinsChannel ?? false}
            disabled={joinsChannel === undefined}
            onChange={(e) => setInChannel(e.target.checked)}
          />
          <span>
            In the fleet channel
            <span className="hint">
              {joinsChannel === undefined
                ? 'Reading the fleet to pick the default…'
                : joinsChannel
                  ? 'Hears what is addressed to it in the fleet channel, and can post there.'
                  : 'Starts outside the fleet channel: nothing posted there reaches it.'}{' '}
              A Telegram bot, if it wants one, is attached on its page once it exists.
            </span>
          </span>
        </label>

        <div>
          <button className="btn primary" onClick={create} disabled={busy || !slugOk || !mission.trim()}>
            {busy ? 'Creating…' : 'Create loop'}
          </button>
        </div>
      </div>
    </div>
  )
}
