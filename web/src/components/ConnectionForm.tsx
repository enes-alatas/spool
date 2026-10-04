import { useState } from 'react'
import { api, Connection } from '../api'
import {
  CONNECTION_SHAPES,
  ConnectionDraft,
  ConnectionShape,
  EMPTY_DRAFT,
  connectionRequest,
  connectionSubmittable,
} from '../connections'

// The create form for a connection, kind first and then that kind's fields
// (#506). On the Connections page, and in a loop's Add connection dialog,
// where what it creates is attached to the loop as well (`onCreated`).

// The secret field's hint, per shape. An env variable's secret is its value,
// so it is required, and write-only like any secret: a username is as hidden
// as a token (Enes, 2026-10-04). An MCP server may not need one.
function secretHint(draft: ConnectionDraft): string {
  const env = draft.env.trim() || 'the env var'
  return draft.shape === 'env'
    ? `What ${env} is set to. Write-only: sent once, never shown again.`
    : 'Optional, for a server that needs a credential. Write-only: sent once, never shown again.'
}

export function ConnectionForm({
  onCreated,
  submitLabel = 'Create connection',
}: {
  onCreated: (created: Connection) => unknown
  submitLabel?: string
}) {
  const [draft, setDraft] = useState<ConnectionDraft>(EMPTY_DRAFT)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (patch: Partial<ConnectionDraft>) => setDraft((d) => ({ ...d, ...patch }))

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      const created = await api.createConnection(connectionRequest(draft))
      setDraft(EMPTY_DRAFT)
      await onCreated(created)
    } catch (e) {
      // The server's own sentence names the field and the rule (#568), so it
      // is shown as it came.
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="form">
      <div className="field">
        <label htmlFor="cn-kind">Kind</label>
        <select
          id="cn-kind"
          value={draft.shape}
          onChange={(e) => set({ shape: e.target.value as ConnectionShape })}
        >
          {CONNECTION_SHAPES.map((s) => (
            <option key={s.value} value={s.value}>
              {s.label}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label htmlFor="cn-name">Name</label>
        <input
          id="cn-name"
          className="mono"
          placeholder="e.g. github"
          value={draft.name}
          onChange={(e) => set({ name: e.target.value.toLowerCase() })}
          autoComplete="off"
          spellCheck={false}
        />
        <div className="hint">a-z, 0-9 and -, up to 32. It can't be changed later.</div>
      </div>
      {draft.shape === 'env' && (
        <div className="field">
          <label htmlFor="cn-env">Variable name</label>
          <input
            id="cn-env"
            className="mono"
            placeholder="GH_TOKEN"
            value={draft.env}
            onChange={(e) => set({ env: e.target.value })}
            autoComplete="off"
            spellCheck={false}
          />
          <div className="hint">The name a loop's tools read it by.</div>
        </div>
      )}
      {draft.shape === 'http' && (
        <div className="field">
          <label htmlFor="cn-url">URL</label>
          <input
            id="cn-url"
            className="mono"
            placeholder="https://mcp.example.com/mcp"
            value={draft.url}
            onChange={(e) => set({ url: e.target.value })}
            autoComplete="off"
            spellCheck={false}
          />
          <div className="hint">Shown back in full, so no credential in it: that goes in the secret.</div>
        </div>
      )}
      {draft.shape === 'stdio' && (
        <>
          <div className="field">
            <label htmlFor="cn-command">Command</label>
            <input
              id="cn-command"
              className="mono"
              placeholder="e.g. npx"
              value={draft.command}
              onChange={(e) => set({ command: e.target.value })}
              autoComplete="off"
              spellCheck={false}
            />
          </div>
          <div className="field">
            <label htmlFor="cn-args">Arguments</label>
            <textarea
              id="cn-args"
              className="mono"
              rows={3}
              placeholder="one per line"
              value={draft.args}
              onChange={(e) => set({ args: e.target.value })}
              spellCheck={false}
            />
          </div>
        </>
      )}

      <div className="field">
        <label htmlFor="cn-secret">{draft.shape === 'env' ? 'Value' : 'Secret (optional)'}</label>
        <input
          id="cn-secret"
          type="password"
          autoComplete="off"
          value={draft.secret}
          onChange={(e) => set({ secret: e.target.value })}
        />
        <div className="hint">{secretHint(draft)}</div>
      </div>

      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div>
        <button className="btn primary" onClick={create} disabled={busy || !connectionSubmittable(draft)}>
          {busy ? 'Creating…' : submitLabel}
        </button>
      </div>
    </div>
  )
}
