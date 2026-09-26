import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type CustomModel, type Settings as SettingsView } from '../api'
import { customModelError, MODEL_LABEL_MAX, rotationGate, tokenSubmittable } from '../forms'
import { customModelNote } from '../options'
import { buildFacts, useClaudeVersion, useVersion } from '../version'
import { loginError } from '../session'

export default function Settings() {
  const { data: settings, error: loadError } = useQuery({ queryKey: ['settings'], queryFn: api.settings })

  return (
    <div className="page measure">
      <h1>Settings</h1>
      <ClaudeToken settings={settings} loadError={loadError} />
      <CustomModels />
      <RotationThresholds settings={settings} loadError={loadError} />
      <SessionSection />
      <Build />
    </div>
  )
}

// Ending the session (#239). The cookie is the credential, so a room left
// open on a shared screen stays open until something clears it — and the hub
// serves the route, so the only thing missing was somewhere to press.
function SessionSection() {
  const qc = useQueryClient()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const signOut = async () => {
    setBusy(true)
    setError('')
    try {
      await api.logout()
      // Resetting is what puts the login page back up: the next probe has no
      // cookie to send, and nothing cached outlives the session that fetched
      // it.
      qc.resetQueries()
    } catch (e) {
      setError(loginError(e))
      setBusy(false)
    }
  }

  return (
    <>
      <h2 className="section-head">Session</h2>
      <p className="page-lede">
        This browser holds a session cookie, not the token. Signing out clears the cookie here; it revokes
        nothing, so the token still opens a new session.
      </p>

      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}

      <button className="btn" onClick={signOut} disabled={busy}>
        {busy ? 'Signing out…' : 'Sign out'}
      </button>
    </>
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

// The sentence for a failed settings load. Both sections that read the
// settings say it in their own place, since each is what an operator came to
// that section to find out.
function loadFailure(loadError: Error): string {
  return `Could not load settings: ${loadError.message}`
}

function ClaudeToken({ settings, loadError }: { settings?: SettingsView; loadError: Error | null }) {
  const qc = useQueryClient()
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // Unknown until the settings arrive. A failed load used to fall through to
  // "Not configured", which asserts a state nobody read and invites pasting
  // a token that is already set (#351).
  const configured = settings?.claude_token_set

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
          {settings ? (
            <div className={`token-state${configured ? ' ok' : ''}`}>
              {configured ? '● Configured' : '○ Not configured'}
            </div>
          ) : loadError ? (
            // the gap .token-state keeps above the input
            <div className="form-error" role="alert" style={{ marginBottom: 8 }}>
              {loadFailure(loadError)}
            </div>
          ) : (
            <div className="token-state">Checking…</div>
          )}
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
            stored write-only: the value never leaves this server or appears in a response.
          </div>
        </div>

        {error && (
          <div className="form-error" role="alert">
            {error}
          </div>
        )}

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
function RotationThresholds({ settings, loadError }: { settings?: SettingsView; loadError: Error | null }) {
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
        that note into a fresh session. These are the two percentages of the model's window that decide when,
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

        {/* The fields are empty and disabled without the stored pair; this
            says why, where an empty field would otherwise read as unset. */}
        {!settings && loadError && (
          <div className="form-error" role="alert">
            {loadFailure(loadError)}
          </div>
        )}
        {error && (
          <div className="form-error" role="alert">
            {error}
          </div>
        )}

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

// The operator's own model ids, offered in every model dropdown after the
// families (#332). Claude Code's picker keeps custom models beside the
// available ones, and this is that list: an older pinned model, or one the
// aliases don't reach, without typing it into Custom… each time. Removing an
// entry changes no loop; the list only feeds the dropdowns.
function CustomModels() {
  const qc = useQueryClient()
  const { data, isPending, error: loadError } = useQuery({ queryKey: ['models'], queryFn: api.models })
  const [model, setModel] = useState('')
  const [label, setLabel] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const refresh = () => qc.invalidateQueries({ queryKey: ['models'] })

  const add = async () => {
    const id = model.trim()
    const invalid = customModelError(id)
    if (invalid) {
      setError(invalid)
      return
    }
    setBusy(true)
    setError('')
    try {
      await api.addCustomModel({ model: id, label: label.trim() })
      setModel('')
      setLabel('')
      refresh()
    } catch (e) {
      // 409 is a duplicate or an alias, and the server's sentence names which.
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  // A hub that predates the list is not a failed load: say what to do.
  const missing = loadError instanceof ApiError && loadError.status === 404

  return (
    <>
      <h2 className="section-head">Custom models</h2>
      <p className="page-lede">
        Model ids offered in every model dropdown, after the families. Removing one changes no loop; a loop
        already on it keeps it.
      </p>

      {missing ? (
        <div className="form-error" role="alert">
          This orchestrator has no model-list API. Update Spool to keep a list here.
        </div>
      ) : loadError ? (
        <div className="form-error" role="alert">
          Could not load the model list: {loadError instanceof Error ? loadError.message : String(loadError)}
        </div>
      ) : isPending ? null : (
        <>
          {data.custom.length > 0 && (
            <div className="model-list">
              {data.custom.map((entry) => (
                <CustomModelRow key={entry.id} entry={entry} onChange={refresh} />
              ))}
            </div>
          )}

          <div className="form">
            <div className="field-row">
              <div className="field">
                <label htmlFor="cm-model">Model id</label>
                <input
                  id="cm-model"
                  className="mono"
                  placeholder="claude-opus-4-1"
                  value={model}
                  disabled={busy}
                  onChange={(e) => setModel(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && add()}
                />
              </div>
              <div className="field">
                <label htmlFor="cm-label">Label (optional)</label>
                <input
                  id="cm-label"
                  placeholder="Opus 4.1 (pinned)"
                  maxLength={MODEL_LABEL_MAX}
                  value={label}
                  disabled={busy}
                  onChange={(e) => setLabel(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && add()}
                />
              </div>
            </div>

            {error && (
              <div className="form-error" role="alert">
                {error}
              </div>
            )}

            <div>
              <button className="btn primary" onClick={add} disabled={busy || !model.trim()}>
                {busy ? 'Adding…' : 'Add model'}
              </button>
            </div>
          </div>
        </>
      )}
    </>
  )
}

// One custom model: its id, its label, what it resolved to, and the two
// edits the API allows. The id itself is not editable: a different id is a
// different entry, so it is deleted and added.
function CustomModelRow({ entry, onChange }: { entry: CustomModel; onChange: () => void }) {
  const [draft, setDraft] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const note = customModelNote(entry)

  const run = async (request: () => Promise<unknown>, after?: () => void) => {
    setBusy(true)
    setError('')
    try {
      await request()
      after?.()
      onChange()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const saveLabel = () =>
    run(
      () => api.relabelCustomModel(entry.id, (draft ?? '').trim()),
      () => setDraft(null),
    )

  return (
    <div className="model-row">
      <div className="model-row-head">
        <code className="model-id">{entry.model}</code>
        {draft === null ? (
          entry.label && <span className="model-label">{entry.label}</span>
        ) : (
          <input
            className="model-label-input"
            aria-label={`Label for ${entry.model}`}
            maxLength={MODEL_LABEL_MAX}
            value={draft}
            autoFocus
            disabled={busy}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') saveLabel()
              if (e.key === 'Escape') setDraft(null)
            }}
          />
        )}
        <span className="model-row-actions">
          {draft === null ? (
            <button className="btn sm" onClick={() => setDraft(entry.label)} disabled={busy}>
              {entry.label ? 'Relabel' : 'Add label'}
            </button>
          ) : (
            <>
              <button className="btn sm" onClick={saveLabel} disabled={busy}>
                Save
              </button>
              <button className="btn sm" onClick={() => setDraft(null)} disabled={busy}>
                Cancel
              </button>
            </>
          )}
          <button
            className="btn sm danger"
            disabled={busy}
            onClick={() => {
              if (confirm(`Remove ${entry.model} from the dropdowns? Loops already on it keep it.`)) {
                run(() => api.deleteCustomModel(entry.id))
              }
            }}
          >
            Remove
          </button>
        </span>
      </div>
      {note && <div className="model-note">{note}</div>}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}
