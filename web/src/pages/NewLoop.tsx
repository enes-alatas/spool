import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import { MODEL_OPTIONS, EFFORT_OPTIONS, PACING_OPTIONS, RUNTIME_OPTIONS, runtimeNote } from '../options'
import { useHubRuntime } from '../version'

export default function NewLoop() {
  const nav = useNavigate()
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [mission, setMission] = useState('')
  const [model, setModel] = useState('')
  const [customModel, setCustomModel] = useState('')
  const [effort, setEffort] = useState('')
  const [pacing, setPacing] = useState<'fixed' | 'self'>('fixed')
  const [runtime, setRuntime] = useState('')
  const [wsPath, setWsPath] = useState('')
  const [wsInfo, setWsInfo] = useState<{ exists: boolean; is_git: boolean } | null>(null)
  const [interval, setIntervalMin] = useState('30')
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // What the hub was started as, and whether it will accept an uncontained
  // loop at all. Both are constants under a running server; the form reads
  // them rather than assuming docker, because on a single-machine install the
  // default is bare and the warning belongs on every creation (#255).
  const { data: health } = useHubRuntime()
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
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
    setBusy(true)
    setError('')
    try {
      const chosenModel = model === '__custom__' ? customModel.trim() : model
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
        tg_bot_token: token.trim() || undefined,
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
        {error && <div className="form-error">{error}</div>}

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
              {MODEL_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
              <option value="__custom__">Custom…</option>
            </select>
            {model === '__custom__' && (
              <input
                style={{ marginTop: 6 }}
                placeholder="exact model id, e.g. claude-opus-4-8"
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
            placeholder="/path/to/repo — leave empty for a conversational loop"
            value={wsPath}
            onChange={(e) => inspect(e.target.value)}
          />
          {wsInfo && wsPath.trim() && (
            <div className={`hint ${wsInfo.exists ? (wsInfo.is_git ? 'ok' : '') : 'warn'}`}>
              {!wsInfo.exists
                ? 'Directory not found.'
                : wsInfo.is_git
                  ? `Git repo — an isolated worktree will be created on branch loop/${name || '<name>'}.`
                  : 'Plain directory — the loop works here directly.'}
            </div>
          )}
        </div>

        <div className="field">
          <label htmlFor="nl-token">Telegram bot token (optional)</label>
          <input
            id="nl-token"
            // masked: the first place a token is entered, straight out of
            // BotFather and usually pasted, often with somebody watching
            type="password"
            autoComplete="off"
            placeholder="123456:ABC-DEF…"
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          <div className="hint">
            Create a bot with @BotFather (/newbot), then: /setprivacy → Disable, so the bot sees group
            messages. Paste its token here and add the bot to your group.
          </div>
        </div>

        <div>
          <button className="btn primary" onClick={create} disabled={busy || !slugOk || !mission.trim()}>
            {busy ? 'Creating…' : 'Create loop'}
          </button>
        </div>
      </div>
    </div>
  )
}
