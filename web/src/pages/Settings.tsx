import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Settings as SettingsView } from '../api'
import { rotationGate, tokenSubmittable } from '../forms'
import { buildFacts, useClaudeVersion, useVersion } from '../version'

export default function Settings() {
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })

  return (
    <div className="page measure">
      <h1>Settings</h1>
      <ClaudeToken settings={settings} />
      <RotationThresholds settings={settings} />
      <Build />
    </div>
  )
}

// Which Spool this room is talking to, and which `claude` it runs (#224).
// Settings is the page a question about the installation is already asked on,
// and it is the only page that shows this: a version on every screen is
// reference detail competing with the work.
function Build() {
  const { data: build } = useVersion()
  const { data: health } = useClaudeVersion()

  return (
    <>
      <h2 className="section-head">Build</h2>
      <p className="page-lede">
        What this server is, for a bug report or for checking that a deploy actually landed.
      </p>

      <dl className="facts">
        {buildFacts(build, health?.claude_version).map((fact) => (
          <div key={fact.label} className="fact">
            <dt>{fact.label}</dt>
            <dd>{fact.value}</dd>
          </div>
        ))}
      </dl>
    </>
  )
}

function ClaudeToken({ settings }: { settings?: SettingsView }) {
  const qc = useQueryClient()
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
    <>
      <h2 className="section-head first">Claude token</h2>
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
            autoComplete="off"
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
            disabled={busy || !tokenSubmittable(token)}
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
    </>
  )
}

// The percentages a loop rotates its context at (ADR-0022). The server holds
// one pair for the fleet and answers with the effective values, defaults
// included, so there is no separate "unset" to render: a field always shows
// the number rotation is actually being judged by.
function RotationThresholds({ settings }: { settings?: SettingsView }) {
  const qc = useQueryClient()
  // Null until the operator types: the stored values are the source of truth
  // and a draft only overlays them, so a save that lands elsewhere is visible
  // here rather than sitting behind a stale copy of itself.
  const [draft, setDraft] = useState<{ arm: string; force: string } | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const stored = settings
    ? {
        arm: String(settings.context_arm_percent),
        force: String(settings.context_force_percent),
      }
    : null
  const { shown, changed, sendable } = rotationGate(stored, draft)

  const edit = (patch: { arm?: string; force?: string }) => {
    if (!shown) return
    setDraft({ ...shown, ...patch })
  }

  const save = async () => {
    if (!draft) return
    setBusy(true)
    setError('')
    try {
      // The PUT answers with the settings it stored, so the cache takes them
      // directly: dropping the draft first means anything short of that would
      // render the stale pair until a refetch landed.
      const saved = await api.setRotationThresholds(Number(draft.arm), Number(draft.force))
      setDraft(null)
      qc.setQueryData(['settings'], saved)
    } catch (e) {
      // The server validates the pair and says which numbers it rejected, so
      // its own sentence is more use inline than anything restated here.
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <h2 className="section-head">Context rotation</h2>
      <p className="page-lede">
        A loop rotates its own context before it runs out of window: it writes a handoff note, then carries
        that note into a fresh session. These are the two percentages of the model's window that decide when —
        fleet-wide, applied to each loop from its next measured turn. No restart.
      </p>

      <div className="form">
        <div className="field-row">
          <div className="field">
            <label htmlFor="rot-arm">Arm at (%)</label>
            <input
              id="rot-arm"
              inputMode="numeric"
              value={shown?.arm ?? ''}
              disabled={!shown || busy}
              onChange={(e) => edit({ arm: e.target.value })}
            />
            <div className="hint">
              Past this, the loop rotates at the end of its next wake with no work queued.
            </div>
          </div>
          <div className="field">
            <label htmlFor="rot-force">Force at (%)</label>
            <input
              id="rot-force"
              inputMode="numeric"
              value={shown?.force ?? ''}
              disabled={!shown || busy}
              onChange={(e) => edit({ force: e.target.value })}
            />
            <div className="hint">
              Past this, it stops waiting for a quiet moment and rotates before more work is delivered.
            </div>
          </div>
        </div>

        {error && <div className="form-error">{error}</div>}

        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn primary" onClick={save} disabled={busy || !sendable}>
            {busy ? 'Saving…' : 'Save thresholds'}
          </button>
          {changed && (
            <button className="btn" onClick={() => setDraft(null)} disabled={busy}>
              Cancel
            </button>
          )}
        </div>
      </div>
    </>
  )
}
