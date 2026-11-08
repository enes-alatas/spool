import { useState, type CSSProperties } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type CustomModel, type Settings as SettingsView } from '../api'
import { customModelError, MODEL_LABEL_MAX, rotationGate } from '../forms'
import { customModelNote } from '../options'
import { buildFacts, useVersion } from '../version'
import { useMay, useSignOut } from '../components/Session'
import { SignOutIcon } from '../components/Icons'
import { EgressSection } from '../components/EgressSection'
import { CloudSection } from '../components/CloudSection'
import { ClaudeLoginSection } from '../components/ClaudeLogin'
import { UsersSection } from '../components/UsersSection'
import { ChatAccess } from '../components/ChatAccess'
import { LinksSection } from '../components/LinksSection'
import { CapBanners, useCloudOnly, useMinuteClock } from '../components/PlanUsage'
import { ManagementTokensSection } from '../components/ManagementTokens'
import { capFields, capGate, capReach, capValue, type CapDraft } from '../planCap'
import {
  ERROR_TURN_STOPS,
  TURN_MINUTE_STOPS,
  WAKE_MINUTE_STOPS,
  SEND_MINUTE_STOPS,
  errorTurnsField,
  errorTurnsValue,
  minutesField,
  minutesNote,
  minutesValue,
  stepField,
  stopIndex,
  stopLabel,
} from '../health'

export default function Settings() {
  const { data: settings, error: loadError } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  const may = useMay()

  return (
    <div className="page measure">
      <h1>Settings</h1>
      <AccountSection />
      {/* An owner's Users section carries the rest of Access under its
          list; an admin, who manages no users, gets that part alone. */}
      {may('manage-users') ? <UsersSection /> : may('manage') && <ChatAccess />}
      {/* Any user adds a Claude login of their own (ADR-0060); its owner or an
          admin acts on it, and only an admin runs the login check. */}
      <ClaudeLoginSection settings={settings} />
      {/* The rest is the hub's own configuration, which only an admin
          changes (#679): a member's Settings is who they are, their Claude
          logins and which Spool this is. */}
      {may('manage') && (
        <>
          <CustomModels />
          <PlanGuardrails settings={settings} loadError={loadError} />
          <HealthCheck settings={settings} loadError={loadError} />
          <RotationThresholds settings={settings} loadError={loadError} />
          <EgressSection />
          <CloudSection />
          <ManagementTokensSection />
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

      {/* A token session is no user, and has no chat account to link. */}
      {me?.via === 'password' && <LinksSection />}
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

// The plan cap's two thresholds (#650, #651): past either window's, the
// loops on that Claude login sleep at the end of their turn and wake when
// that window resets (#1075). The same two apply to every login. The lines
// over them say whether each login's cap has fired, and while one has,
// Resume now lifts it until the reset without touching the numbers.
function PlanGuardrails({ settings, loadError }: { settings?: SettingsView; loadError: Error | null }) {
  const qc = useQueryClient()
  const { data: usages } = useQuery({ queryKey: ['plan-usage'], queryFn: api.planUsage })
  // Which logins the cap acts on (#1303): a login whose loops all run in
  // the cloud gets no new reading, so it is acted on only while its last
  // one counts.
  const cloudOnly = useCloudOnly()
  const reach = capReach(usages?.logins, cloudOnly)
  // As the rotation pair: the stored values are the truth, a draft overlays.
  const [draft, setDraft] = useState<CapDraft | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const stored = settings
    ? capFields(settings.plan_cap_five_hour_percent, settings.plan_cap_seven_day_percent)
    : null
  const { shown, changed, sendable } = capGate(stored, draft)
  const now = useMinuteClock()

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
        When a Claude login's plan usage reaches a threshold, the loops on that login sleep at the end of
        their turn and wake when that window resets; the thresholds apply to every login. Nothing is lost:
        workstations stay up and messages wait in the inbox. Until a loop has reported a login's usage, its
        cap does not fire.
      </p>

      <div className="form">
        <CapBanners usages={usages} now={now} idle />
        {reach && <div className="cap-reach">{reach}</div>}

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
              At this, a login's loops sleep until its 5-hour window resets. Empty turns it off.
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
              At this, a login's loops sleep until its weekly window resets. Empty turns it off.
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

// The health check's settings, by their names in the settings view, and how
// Settings shows and reads each one: the slider's steps and the ones it
// labels, and the number field's largest value.
type HealthKey =
  'health_error_turns' | 'health_turn_minutes' | 'health_wake_grace_minutes' | 'health_send_grace_minutes'

const HEALTH_FIELDS: {
  key: HealthKey
  id: string
  label: string
  unit: string
  hint: string
  minutes: boolean
  max: number
  stops: number[]
  ticks: number[]
  field: (stored: number) => string
  value: (field: string) => number | undefined
}[] = [
  {
    key: 'health_error_turns',
    id: 'health-error-turns',
    label: 'Errored turns',
    unit: 'in a row',
    hint: 'Turns ending in an error, one after another.',
    minutes: false,
    max: 100,
    stops: ERROR_TURN_STOPS,
    ticks: [0, 5, 10, 100],
    field: errorTurnsField,
    value: errorTurnsValue,
  },
  {
    key: 'health_turn_minutes',
    id: 'health-turn-minutes',
    label: 'Long turn',
    unit: 'min',
    hint: 'A turn running this long. Kill process ends it.',
    minutes: true,
    max: 1440,
    stops: TURN_MINUTE_STOPS,
    ticks: [0, 60, 240, 1440],
    field: minutesField,
    value: minutesValue,
  },
  {
    key: 'health_wake_grace_minutes',
    id: 'health-wake-grace-minutes',
    label: 'Missed wake',
    unit: 'min',
    hint: 'A wake that starts no turn this long.',
    minutes: true,
    max: 1440,
    stops: WAKE_MINUTE_STOPS,
    ticks: [0, 10, 60, 1440],
    field: minutesField,
    value: minutesValue,
  },
  {
    key: 'health_send_grace_minutes',
    id: 'health-send-grace-minutes',
    label: 'Failed send',
    unit: 'min',
    hint: 'A message the loop sent, undelivered this long.',
    minutes: true,
    max: 1440,
    stops: SEND_MINUTE_STOPS,
    ticks: [0, 5, 60, 1440],
    field: minutesField,
    value: minutesValue,
  },
]

// The health check's settings (ADR-0053): how many errored turns in a row
// open a problem with a loop, how many minutes one turn runs before it does
// (#1142), how many a wake the loop owes waits for a turn (#1143), and how
// many a message it sent stays undelivered (#1144). A workstation down past
// its grace period opens one whatever these say.
//
// Each is a line saying its value and what it watches; Edit opens it in
// place (#1147) into a slider for the common values and a number for an
// exact one (#1146): dragging snaps to steps, ←/→ on the slider move one
// step, and ↑/↓ in the number move it by one. One line is open at a time,
// and a closed line shows its draft, so Save sends every edit made.
function HealthCheck({ settings, loadError }: { settings?: SettingsView; loadError: Error | null }) {
  const qc = useQueryClient()
  // As the other sections: the stored values are the truth, a draft overlays.
  // Each field has its own, so editing one leaves the others on the stored value.
  const [drafts, setDrafts] = useState<Partial<Record<HealthKey, string>>>({})
  const [open, setOpen] = useState<HealthKey | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // Only the fields drafted go in the write, so an untouched one is left out.
  // A draft that is not a value the hub takes holds Save back, whichever
  // field it is in.
  const body: Partial<Record<HealthKey, number>> = {}
  let valid = true
  let changed = false
  for (const f of HEALTH_FIELDS) {
    const draft = drafts[f.key]
    if (draft === undefined) continue
    const value = f.value(draft)
    if (value === undefined) {
      valid = false
      continue
    }
    body[f.key] = value
    if (value !== settings?.[f.key]) changed = true
  }
  const sendable = valid && changed
  const drafting = Object.keys(drafts).length > 0
  const draft = (key: HealthKey, text: string) => setDrafts((d) => ({ ...d, [key]: text }))

  const save = async () => {
    if (!sendable) return
    setBusy(true)
    setError('')
    try {
      const saved = await api.setHealthCheck(body)
      setDrafts({})
      setOpen(null)
      qc.setQueryData(['settings'], saved)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section>
      <h2 className="section-head">Health check</h2>
      <p className="page-lede">
        The hub tells a loop's owner once when a problem opens with it, and once when it clears. Fleet marks
        the loop, and its page says what is wrong. A workstation down opens one whatever these say.
      </p>
      <div className="health-limits">
        {HEALTH_FIELDS.map((f) => {
          const shown = drafts[f.key] ?? (settings ? f.field(settings[f.key]) : null)
          const value = shown === null ? undefined : f.value(shown)
          const off = value === 0
          const note = f.minutes && value !== undefined ? minutesNote(value) : ''
          const last = f.stops.length - 1
          if (open !== f.key)
            return (
              <div className="health-row" key={f.key}>
                <span className="health-row-label">
                  {f.label}
                  {drafts[f.key] !== undefined && <span className="health-row-edited">edited</span>}
                </span>
                <span className={`health-row-value${off ? ' off' : ''}`}>
                  {shown === null ? '…' : value === undefined ? shown : off ? 'off' : value}
                  {!off && value !== undefined && <span className="health-limit-unit">{f.unit}</span>}
                  {note && <span className="health-limit-note">{note}</span>}
                </span>
                <span className="health-row-hint">{f.hint}</span>
                <button
                  type="button"
                  className="health-row-edit"
                  disabled={shown === null || busy}
                  aria-label={`Edit ${f.label}`}
                  onClick={() => setOpen(f.key)}
                >
                  Edit
                </button>
              </div>
            )
          return (
            <div className={`health-limit${off ? ' off' : ''}`} key={f.key}>
              <div className="health-limit-head">
                <label htmlFor={f.id}>{f.label}</label>
                <button type="button" className="health-row-edit" onClick={() => setOpen(null)}>
                  Done
                </button>
              </div>
              <div className="health-limit-value">
                <input
                  id={f.id}
                  // opened by Edit, so the number takes the keys at once
                  autoFocus
                  inputMode="numeric"
                  placeholder="off"
                  value={shown ?? ''}
                  disabled={shown === null || busy}
                  aria-invalid={value === undefined && shown !== null}
                  onChange={(e) => draft(f.key, e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Escape') setOpen(null)
                    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
                    e.preventDefault()
                    draft(f.key, stepField(shown ?? '', e.key === 'ArrowUp' ? 1 : -1, f.max))
                  }}
                  // as wide as what it holds, so the unit sits beside the number
                  style={{ width: `${Math.max((shown || 'off').length, 2) + 1}ch` }}
                />
                {!off && <span className="health-limit-unit">{f.unit}</span>}
                {note && <span className="health-limit-note">{note}</span>}
              </div>
              <input
                type="range"
                min={0}
                max={last}
                step={1}
                value={stopIndex(f.stops, value ?? 0)}
                disabled={shown === null || busy}
                aria-label={`${f.label}, in steps`}
                aria-valuetext={value ? `${value} ${f.unit}` : 'off'}
                style={{ '--lit': `${(stopIndex(f.stops, value ?? 0) / last) * 100}%` } as CSSProperties}
                onChange={(e) => draft(f.key, f.field(f.stops[Number(e.target.value)]))}
              />
              <div className="health-limit-ticks" aria-hidden="true">
                {f.ticks.map((tick) => (
                  <span key={tick} style={{ left: `${(stopIndex(f.stops, tick) / last) * 100}%` }}>
                    {stopLabel(tick, f.minutes)}
                  </span>
                ))}
              </div>
              <div className="hint">{f.hint}</div>
            </div>
          )
        })}
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
          {busy ? 'Saving…' : 'Save'}
        </button>
        {drafting && (
          <button
            className="btn"
            onClick={() => {
              setDrafts({})
              setOpen(null)
            }}
            disabled={busy}
          >
            Cancel
          </button>
        )}
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
