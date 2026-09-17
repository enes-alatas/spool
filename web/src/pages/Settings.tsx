import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'

export default function Settings() {
  const qc = useQueryClient()
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const configured = settings?.claude_token_set ?? false

  const submit = async (value: string) => {
    setBusy(true)
    setError('')
    try {
      await api.setClaudeToken(value)
      setToken('')
      qc.invalidateQueries({ queryKey: ['settings'] })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="page measure">
      <h1>Settings</h1>
      <p className="page-lede">
        Workstation (contained) loops run <code>claude</code> inside a Docker container that has no login of
        its own. Paste a long-lived token from <code>claude setup-token</code> and every workstation runs
        under your own Claude plan and limits. Bare loops keep using this machine's login and don't need it.
      </p>

      <div className="form">
        <div className="field">
          <label htmlFor="claude-token">Claude setup-token</label>
          <div className={`token-state${configured ? ' ok' : ''}`}>
            {configured ? '● Configured' : '○ Not configured'}
          </div>
          <input
            id="claude-token"
            type="password"
            placeholder={configured ? 'Paste a new token to replace it' : 'sk-ant-oat01-…'}
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          <div className="hint">
            Run <code>claude setup-token</code> where you're logged in, then paste the result here. It's
            stored write-only — the value never leaves this server or appears in a response.
          </div>
        </div>

        {error && <div className="form-error">{error}</div>}

        <div style={{ display: 'flex', gap: 8 }}>
          <button
            className="btn primary"
            onClick={() => submit(token.trim())}
            disabled={busy || !token.trim()}
          >
            {busy ? 'Saving…' : configured ? 'Replace token' : 'Save token'}
          </button>
          {configured && (
            <button className="btn danger" onClick={() => submit('')} disabled={busy}>
              Clear
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
