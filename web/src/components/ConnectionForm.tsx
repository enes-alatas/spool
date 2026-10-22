import { useState } from 'react'
import { api, Connection } from '../api'
import {
  ConnectionDraft,
  ConnectionShape,
  EMPTY_DRAFT,
  connectionRequest,
  connectionShapes,
  connectionSubmittable,
} from '../connections'

// The create form for a connection, kind first and then that kind's fields
// (#506). On the Connections page, and in a loop's Add connection dialog,
// where what it creates is attached to the loop as well (`onCreated`). A
// cloud loop's dialog offers no GitHub App, which it couldn't hold.

// The secret field's hint, per shape. An env variable's secret is its value,
// so it is required, and write-only like any secret: a username is as hidden
// as a token (Enes, 2026-10-04). An MCP server may not need one.
function secretHint(draft: ConnectionDraft): string {
  const env = draft.env.trim() || 'the env var'
  return draft.shape === 'env'
    ? `What ${env} is set to. Write-only: sent once, never shown again.`
    : 'Optional, for a server that needs a credential. Write-only: sent once, never shown again.'
}

// What a GitHub App connection does with its key (ADR-0043), said
// before the operator hands it over. The token helper (#1054) is what
// fetches a token for a loop's git and gh.
export const GITHUB_APP_NOTE =
  "The key stays in the hub: no loop is ever given it. A loop holding this connection is handed the app's installation tokens instead, each lasting an hour: the loop's git and gh fetch a fresh one on their own when they need it."

export function ConnectionForm({
  onCreated,
  submitLabel = 'Create connection',
  cloud = false,
}: {
  onCreated: (created: Connection) => unknown
  submitLabel?: string
  cloud?: boolean
}) {
  const [draft, setDraft] = useState<ConnectionDraft>(EMPTY_DRAFT)
  // The .pem file the key was read from, when it came from one: the key is
  // then not drawn in the field at all.
  const [keyFile, setKeyFile] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (patch: Partial<ConnectionDraft>) => setDraft((d) => ({ ...d, ...patch }))

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      const created = await api.createConnection(connectionRequest(draft))
      setDraft(EMPTY_DRAFT)
      setKeyFile('')
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
          {connectionShapes(cloud).map((s) => (
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

      {draft.shape === 'github' && (
        <>
          <div className="field">
            <label htmlFor="cn-app">App ID or Client ID</label>
            <input
              id="cn-app"
              className="mono"
              placeholder="e.g. 123456"
              value={draft.appId}
              onChange={(e) => set({ appId: e.target.value })}
              autoComplete="off"
              spellCheck={false}
            />
            <div className="hint">On the app's settings page on GitHub, under About.</div>
          </div>
          <div className="field">
            <label htmlFor="cn-installation">Installation ID</label>
            <input
              id="cn-installation"
              className="mono"
              inputMode="numeric"
              placeholder="e.g. 51234567"
              value={draft.installationId}
              onChange={(e) => set({ installationId: e.target.value })}
              autoComplete="off"
              spellCheck={false}
            />
            <div className="hint">The number that ends the installation's settings URL on GitHub.</div>
          </div>
          <PrivateKeyField
            value={draft.secret}
            file={keyFile}
            onChange={(secret, file) => {
              set({ secret })
              setKeyFile(file)
            }}
          />
        </>
      )}

      {draft.shape !== 'github' && (
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
      )}

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

// A GitHub App's private key: the .pem file GitHub downloads, chosen or
// pasted. A chosen file is read here and never drawn; a pasted key is in a
// field of its own, many lines long as a key is. Either way it is sent once
// and never shown again.
function PrivateKeyField({
  value,
  file,
  onChange,
}: {
  value: string
  file: string
  onChange: (secret: string, file: string) => void
}) {
  const [error, setError] = useState('')
  const choose = async (picked: File | undefined) => {
    setError('')
    if (!picked) return
    try {
      onChange(await picked.text(), picked.name)
    } catch (e) {
      setError(`Could not read ${picked.name}: ${e instanceof Error ? e.message : String(e)}`)
    }
  }
  return (
    <div className="field">
      <label htmlFor="cn-key">Private key</label>
      {file ? (
        <div className="key-file">
          <span className="mono">{file}</span> read, ready to send.
          <button type="button" className="text-button" onClick={() => onChange('', '')}>
            Clear
          </button>
        </div>
      ) : (
        <>
          <textarea
            id="cn-key"
            className="mono key-paste"
            rows={4}
            placeholder="paste the whole .pem file"
            value={value}
            onChange={(e) => onChange(e.target.value, '')}
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
          />
          <label className="btn sm key-choose">
            Choose the .pem file
            <input type="file" accept=".pem" onChange={(e) => choose(e.target.files?.[0])} />
          </label>
        </>
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="hint">
        Write-only: sent once, never shown again. A key that isn't an RSA key in PEM is refused.
      </div>
      <div className="hint">{GITHUB_APP_NOTE}</div>
    </div>
  )
}
