import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import { MODEL_OPTIONS, EFFORT_OPTIONS, PACING_OPTIONS } from '../options'

export default function NewLoop() {
  const nav = useNavigate()
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [mission, setMission] = useState('')
  const [model, setModel] = useState('')
  const [customModel, setCustomModel] = useState('')
  const [effort, setEffort] = useState('')
  const [pacing, setPacing] = useState<'fixed' | 'self'>('fixed')
  const [wsPath, setWsPath] = useState('')
  const [wsInfo, setWsInfo] = useState<{ exists: boolean; is_git: boolean } | null>(null)
  const [interval, setIntervalMin] = useState('30')
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

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
    <div className="page">
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
          <div className="hint">
            Lowercase, 2–32 chars. Others reach this loop as @{name || 'name'}.
          </div>
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
            placeholder="123456:ABC-DEF…"
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          <div className="hint">
            Create a bot with @BotFather (/newbot), then: /setprivacy → Disable, so the bot sees
            group messages. Paste its token here and add the bot to your group.
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
