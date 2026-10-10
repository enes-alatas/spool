import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type CustomModel, type Settings as SettingsView } from '../api'
import { customModelError, MODEL_LABEL_MAX, rotationGate, tokenSubmittable } from '../forms'
import { customModelNote } from '../options'
import { buildFacts, useVersion } from '../version'
import { HarnessCheck } from '../components/HarnessCheck'
import { useMay, useSignOut } from '../components/Session'
import { SignOutIcon } from '../components/Icons'
import { EgressSection } from '../components/EgressSection'
import { loginCheckTone } from '../onboarding'
import { CapBanner, useMinuteClock } from '../components/PlanUsage'
import { capFields, capGate, capIdleStatus, capReason, capValue, type CapDraft } from '../planCap'

export default function Settings() {
  const { data: settings, error: loadError } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  const may = useMay()

  return (
    <div className="page measure">
      <h1>Settings</h1>
      <AccountSection />
      {/* The rest is the hub's own configuration, which only an admin
          changes (#679): a member's Settings is who they are and which
          Spool this is. */}
      {may('manage') && (
        <>
          <ClaudeToken settings={settings} loadError={loadError} />
          <CustomModels />
          <PlanGuardrails settings={settings} loadError={loadError} />
          <RotationThresholds settings={settings} loadError={loadError} />
          <EgressSection />
        </>
      )}
      <Build />
    </div>
  )
}

// Who this session is (#674), and the way out. It lives here rather than in
// the top bar, which stays the destinations and New loop. A session the
// operator token opened belongs to no user and acts as the owner.
function AccountSection() {
  const { data: me } = useQuery({ queryKey: ['me'], queryFn: api.me })
  const { signOut, busy, error } = useSignOut()

  return (
    <>
      <h2 className="section-head first">Account</h2>
      <div className="account">
        {me && (
          <>
            {/* The name's first letter. A token session has no name, so it has
                no letter either. */}
            {me.via === 'password' && (
              <span className="account-initial" aria-hidden>
                {me.name.charAt(0)}
              </span>
            )}
            <span className="account-who">
              <span className="account-name">{me.via === 'token' ? 'operator token' : me.name}</span>
              <span className="account-role">{me.role}</span>
            </span>
          </>
        )}
        {/* Signing out ends this browser's session only; the user's other
            sessions stay signed in. */}
        <button className="btn account-sign-out" onClick={signOut} disabled={busy}>
          <SignOutIcon />
          {busy ? 'Signing out…' : 'Sign out'}
        </button>
      </div>

      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </>
  )
}

// Which Spool this room is talking to, and which `claude` it runs (#224).
// Settings is the page a question about the installation is already asked on,
// and it is the only page that shows this: a version on every screen is
// reference detail competing with the work.
function Build() {
  const { data: build } = useVersion()
  // The Claude CLI rides the settings read, behind the credential (#258).
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })

  return (
    <>
      <h2 className="section-head">Build</h2>
      <p className="page-lede">
        What this server is, for a bug report or for checking that a deploy actually landed.
      </p>

      <dl className="facts">
        {buildFacts(build, settings?.claude_version).map((fact) => (
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
      <h2 className="section-head">Claude token</h2>
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

        <LoginCheck />
      </div>
    </>
  )
}

// The harness's last login check (ADR-0044), as the onboarding read words
// it, and a way to run one. Polled quickly only while a check runs.
function LoginCheck() {
  const { data } = useQuery({
    queryKey: ['onboarding'],
    queryFn: api.onboarding,
    refetchInterval: (q) => (q.state.data?.harness.checking ? 2000 : false),
    retry: false,
  })
  const harness = data?.harness
  if (!harness) return null
  return (
    <div className="login-check">
      <span className={`login-check-state ${loginCheckTone(harness)}`}>
        {harness.reason ?? (harness.done ? 'the login works' : 'the login is not checked yet')}
      </span>
      <HarnessCheck pillar={harness} />
    </div>
  )
}

// The plan cap's two thresholds (#650, #651): past either window's, every
// loop sleeps at the end of its turn and wakes when that window resets. The
// status line over them says whether the cap has fired, and while it has,
// Resume now lifts it until the reset without touching the numbers.
function PlanGuardrails({ settings, loadError }: { settings?: SettingsView; loadError: Error | null }) {
  const qc = useQueryClient()
  const { data: usage } = useQuery({ queryKey: ['plan-usage'], queryFn: api.planUsage })
  // As the rotation pair: the stored values are the truth, a draft overlays.
  const [draft, setDraft] = useState<CapDraft | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const stored = settings
    ? capFields(settings.plan_cap_five_hour_percent, settings.plan_cap_seven_day_percent)
    : null
  const { shown, changed, sendable } = capGate(stored, draft)
  const now = useMinuteClock()
  const capped = capReason(usage, now)
  const idle = capIdleStatus(usage, now)

  const edit = (patch: Partial<CapDraft>) => {
    if (!shown) return
    setDraft({ ...shown, ...patch })
  }

  const save = async () => {
    if (!draft) return
    setBusy(true)
    setError('')
    try {
      // Raising or clearing a threshold re-checks the cap and may wake the
      // fleet, so the plan read and the loops are read again with it.
      const saved = await api.setPlanCaps(capValue(draft.fiveHour) ?? 0, capValue(draft.sevenDay) ?? 0)
      setDraft(null)
      qc.setQueryData(['settings'], saved)
      void qc.invalidateQueries({ queryKey: ['plan-usage'] })
      void qc.invalidateQueries({ queryKey: ['loops'] })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="plan-guardrails">
      <h2 className="section-head">Plan guardrails</h2>
      <p className="page-lede">
        When the Claude plan's usage reaches a threshold, every loop sleeps at the end of its turn and wakes
        when that window resets. Nothing is lost: workstations stay up and messages wait in the inbox. Until a
        loop has reported the plan's usage, the cap does not fire.
      </p>

      <div className="form">
        {capped ? <CapBanner reason={capped} /> : idle && <div className="cap-idle">{idle}</div>}

        <div className="field-row">
          <div className="field">
            <label htmlFor="cap-five-hour">5-hour window (%)</label>
            <input
              id="cap-five-hour"
              inputMode="numeric"
              placeholder="off"
              value={shown?.fiveHour ?? ''}
              disabled={!shown || busy}
              onChange={(e) => edit({ fiveHour: e.target.value })}
            />
            <div className="hint">
              At this, loops sleep until the 5-hour window resets. Empty turns it off.
            </div>
          </div>
          <div className="field">
            <label htmlFor="cap-seven-day">7-day window (%)</label>
            <input
              id="cap-seven-day"
              inputMode="numeric"
              placeholder="off"
              value={shown?.sevenDay ?? ''}
              disabled={!shown || busy}
              onChange={(e) => edit({ sevenDay: e.target.value })}
            />
            <div className="hint">
              At this, loops sleep until the weekly window resets. Empty turns it off.
            </div>
          </div>
        </div>

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
    </section>
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
