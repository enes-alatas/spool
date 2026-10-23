import { useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type CloudSettings } from '../api'
import {
  accountState,
  addressState,
  checkRows,
  environmentOptions,
  environmentState,
  FUNNEL_BESIDE_PROXY_DOCS,
  firstOpenStep,
  isQuickTunnel,
  isQuickTunnelHost,
  listenerPort,
  type CheckRow,
  type SetupStep,
  type Tone,
} from '../cloud'
import { formatAgo } from '../format'
import { CheckIcon, RefreshIcon } from './Icons'
import { Copyable } from './Copyable'
import { StepDialog } from './StepDialog'
import { useMinuteClock } from './PlanUsage'
import { SignedIn, SignInSteps } from './ClaudeLogin'
import { Marked } from './Marked'

// Settings → Cloud (ADR-0056, #865), an admin's: where the hub stands on
// running cloud loops, one line a step. A step that is done folds to its line; one that
// needs something opens under it. Setting a step up happens in the guided
// setup, which this page opens at that step.
export function CloudSection() {
  const { data: view, error: loadError } = useQuery({ queryKey: ['cloud'], queryFn: api.cloudSettings })
  const [open, setOpen] = useState<SetupStep | null>(null)

  // A hub from before #864 has no such route.
  const missing = loadError instanceof ApiError && loadError.status === 404

  return (
    <section className="cloud">
      <h2 className="section-head">Cloud</h2>
      <p className="page-lede">
        A cloud loop runs in a Claude Code cloud session on Anthropic's machines, so it keeps running while
        this machine sleeps. It reaches this hub over the internet, so the hub needs a listener for it and a
        public address, and it runs on the hub's Claude login. The hub checks each step.
      </p>
      {missing ? (
        <div className="form-error" role="alert">
          This hub has no cloud setup. Update Spool to run cloud loops.
        </div>
      ) : loadError ? (
        <div className="form-error" role="alert">
          Could not load the cloud setup: {loadError instanceof Error ? loadError.message : String(loadError)}
        </div>
      ) : !view ? null : (
        <>
          <div className="cloud-steps">
            <ListenerLine view={view} onOpen={() => setOpen('listener')} />
            <AddressLine view={view} onOpen={() => setOpen('address')} />
            <AccountLine view={view} onOpen={() => setOpen('account')} />
            <EnvironmentLine view={view} onOpen={() => setOpen('environment')} />
          </div>
          {firstOpenStep(view) !== 'done' && (
            <div className="cloud-actions">
              <button className="btn primary" onClick={() => setOpen(firstOpenStep(view))}>
                Set up cloud loops
              </button>
            </div>
          )}
        </>
      )}
      {open && view && <CloudSetup start={open} onClose={() => setOpen(null)} />}
    </section>
  )
}

function StepLine({
  title,
  tone,
  value,
  action,
  children,
}: {
  title: string
  tone: Tone
  value?: string
  action?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className={`cloud-step ${tone}`}>
      <span className="cloud-dot" aria-hidden />
      <span className="cloud-step-title">{title}</span>
      {value && <span className="cloud-step-value">{value}</span>}
      <span className="cloud-step-end">
        {children}
        {action}
      </span>
    </div>
  )
}

function ListenerLine({ view, onOpen }: { view: CloudSettings; onOpen: () => void }) {
  const { listener } = view
  return (
    <StepLine
      title="Cloud listener"
      tone={listener.on ? 'ok' : 'todo'}
      value={listener.addr}
      action={
        <button className="btn sm" onClick={onOpen}>
          {listener.on ? 'Change' : 'Set up'}
        </button>
      }
    >
      <span className="cloud-step-state">
        {listener.on ? (listener.from_flag ? 'on, from --cloud-listen' : 'on') : 'off'}
      </span>
    </StepLine>
  )
}

function AddressLine({ view, onOpen }: { view: CloudSettings; onOpen: () => void }) {
  const state = addressState(view)
  const now = useMinuteClock()
  const failing = view.check?.state === 'failed'
  return (
    <>
      <StepLine
        title="Public address"
        tone={state.tone}
        value={view.address || undefined}
        action={
          <button className="btn sm" onClick={onOpen}>
            {view.address ? 'Change' : 'Set up'}
          </button>
        }
      >
        <span className="cloud-step-state">
          {state.text}
          {view.check && ` · ${formatAgo(view.check.checked_at, now)}`}
        </span>
      </StepLine>
      {(failing || view.warning) && (
        <div className="cloud-step-open">
          {failing && <CheckRows view={view} />}
          {view.warning === 'quick_tunnel' && <QuickTunnelWarning />}
        </div>
      )}
    </>
  )
}

// The hub's one Claude login (ADR-0058), as far as cloud loops go: its
// state, and a way up to Settings → Claude login, where it is checked and
// signed out. A login not signed in opens the setup at this step.
function AccountLine({ view, onOpen }: { view: CloudSettings; onOpen: () => void }) {
  const state = accountState(view)
  return (
    <StepLine
      title="Claude login"
      tone={state.tone}
      action={
        view.account.set ? (
          <a className="btn sm" href="#claude-login">
            Claude login ↑
          </a>
        ) : (
          <button className="btn sm" onClick={onOpen}>
            Set up
          </button>
        )
      }
    >
      <span className="cloud-step-state">{state.text}</span>
    </StepLine>
  )
}

// The shared environment (ADR-0059): its ID, and whether the
// allowlist pasted into it is the current one. Until it is, cloud loops run
// on whatever the environment allows, which the warning under it says.
function EnvironmentLine({ view, onOpen }: { view: CloudSettings; onOpen: () => void }) {
  const state = environmentState(view)
  return (
    <>
      <StepLine
        title="Environment"
        tone={state.tone}
        value={view.environment.name || view.environment.id || undefined}
        action={
          <button className="btn sm" onClick={onOpen}>
            {view.environment.id ? 'Change' : 'Set up'}
          </button>
        }
      >
        <span className="cloud-step-state">{state.text}</span>
      </StepLine>
      {state.tone === 'warn' && (
        <div className="cloud-step-open">
          <UnconfirmedAllowlistWarning />
        </div>
      )}
    </>
  )
}

// Cloud loops follow the environment's network access, not this hub's
// allowlist, so a list not confirmed since it last changed needs pasting
// again: an allowlist edit, a new address or another environment drops the
// confirm.
function UnconfirmedAllowlistWarning() {
  return (
    <div className="cloud-warning">
      Cloud loops can reach only what the environment's settings on claude.ai allow. Paste the current list
      there, then confirm.
    </div>
  )
}

function CheckRows({ view }: { view: CloudSettings }) {
  const rows = checkRows(view.check, view.address)
  const failing = rows.find((row) => row.tone === 'bad')
  return (
    <>
      <ul className="cloud-check" aria-label="The address check">
        {rows.map((row) => (
          <CheckLine key={row.key} row={row} />
        ))}
      </ul>
      {failing?.hint && (
        <div className="cloud-check-hint">
          {failing.hint}
          {failing.docs && (
            <>
              {' '}
              <a href={failing.docs} target="_blank" rel="noreferrer">
                How, with Caddy
              </a>
            </>
          )}
        </div>
      )}
      {view.check?.detail && <div className="cloud-check-detail">{view.check.detail}</div>}
    </>
  )
}

function CheckLine({ row }: { row: CheckRow }) {
  return (
    <li className={row.tone}>
      <span className="cloud-mark" aria-hidden>
        {row.tone === 'ok' ? '✓' : row.tone === 'bad' ? '✕' : '·'}
      </span>
      {row.text}
      <span className="sr-only">
        {row.tone === 'ok' ? ': passed' : row.tone === 'bad' ? ': failed here' : ': not reached'}
      </span>
    </li>
  )
}

function QuickTunnelWarning() {
  return (
    <div className="cloud-warning">
      A quick tunnel's name lasts only while the tunnel runs. Once it stops, the name can go to someone else,
      and a cloud loop in the middle of a turn would send its token there. For loops you keep, use a named
      tunnel on your own domain.
    </div>
  )
}

// The guided setup (#865): each step the hub checks, in order, opened at the
// one the operator asked for. A step moves on only once the hub says it holds.
const STEPS: { key: SetupStep; label: string }[] = [
  { key: 'listener', label: 'Cloud listener' },
  { key: 'address', label: 'Public address' },
  { key: 'account', label: 'Claude login' },
  { key: 'environment', label: 'Environment' },
]

export function CloudSetup({
  start,
  doneLabel = 'Done',
  onClose,
}: {
  start: SetupStep
  // what the last step's button says: where closing the setup returns to
  doneLabel?: string
  onClose: () => void
}) {
  const { data: view } = useQuery({ queryKey: ['cloud'], queryFn: api.cloudSettings })
  const [step, setStep] = useState<SetupStep>(start)
  const [busy, setBusy] = useState(false)
  if (!view) return null
  const at = STEPS.findIndex((s) => s.key === step)

  return (
    <StepDialog title="Set up cloud loops" className="cloud-setup" busy={busy} onClose={onClose}>
      <ol className="cloud-stepper">
        {STEPS.map((s, i) => {
          const done = step === 'done' || i < at
          return (
            <li
              key={s.key}
              className={done ? 'done' : i === at ? 'now' : ''}
              aria-current={i === at ? 'step' : undefined}
            >
              <span className="cloud-stepper-n">{done ? <CheckIcon size={11} /> : i + 1}</span>
              {s.label}
            </li>
          )
        })}
      </ol>
      <div className="form">
        {step === 'listener' && (
          <ListenerStep view={view} setBusy={setBusy} onNext={() => setStep('address')} />
        )}
        {step === 'address' && (
          <AddressStep
            view={view}
            setBusy={setBusy}
            onBack={() => setStep('listener')}
            onNext={() => setStep('account')}
          />
        )}
        {step === 'account' && (
          <AccountStep
            view={view}
            setBusy={setBusy}
            onBack={() => setStep('address')}
            onNext={() => setStep('environment')}
          />
        )}
        {step === 'environment' && (
          <EnvironmentStep
            view={view}
            setBusy={setBusy}
            onBack={() => setStep('account')}
            onNext={() => setStep('done')}
          />
        )}
        {step === 'done' && (
          <>
            <p className="step-dialog-state" aria-live="polite">
              <span className="ok">
                <CheckIcon size={14} /> The listener is on, the address reaches this hub, the Claude login is
                signed in, and the environment holds this hub's allowlist.
              </span>
            </p>
            <p className="step-dialog-lede">
              The hub checks the address every five minutes and the login each time it starts, and Settings →
              Cloud shows where each step stands.
            </p>
            <form method="dialog" className="pillar-actions">
              <button className="btn primary">{doneLabel}</button>
            </form>
          </>
        )}
      </div>
    </StepDialog>
  )
}

// A request that answers with the whole setup, which the page then shows.
function useCloudChange(setBusy: (busy: boolean) => void) {
  const qc = useQueryClient()
  const [error, setError] = useState('')
  const run = async (request: () => Promise<CloudSettings>) => {
    setBusy(true)
    setError('')
    try {
      const view = await request()
      qc.setQueryData(['cloud'], view)
      return view
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return undefined
    } finally {
      setBusy(false)
    }
  }
  return { run, error }
}

function ListenerStep({
  view,
  setBusy,
  onNext,
}: {
  view: CloudSettings
  setBusy: (busy: boolean) => void
  onNext: () => void
}) {
  const { run, error } = useCloudChange(setBusy)
  const [listen, setListen] = useState('')
  const [working, setWorking] = useState(false)
  const { listener } = view
  const turn = async (on: boolean) => {
    setWorking(true)
    await run(() => api.switchCloudListener(on, on ? listen.trim() : ''))
    setWorking(false)
  }

  return (
    <>
      <p className="step-dialog-lede">
        A cloud loop runs on Anthropic's machines, not this one, so it calls this hub over the internet to
        send a message. The hub answers on a listener of its own, which serves the loops' one tool and the
        posts their sessions make when one starts and when a turn ends, and nothing else: the control room
        stays private.
      </p>
      {listener.on ? (
        <>
          <p className="step-dialog-state" aria-live="polite">
            <span className="ok">
              <CheckIcon size={14} /> The cloud listener is on, at {listener.addr}.
            </span>
          </p>
          {listener.from_flag && (
            <p className="cloud-small">
              <Copyable text="--cloud-listen" /> opened it, so it closes only when the hub restarts without
              the flag.
            </p>
          )}
        </>
      ) : (
        <div className="field">
          <label htmlFor="cloud-listen">Listen on</label>
          <input
            id="cloud-listen"
            className="mono"
            placeholder="127.0.0.1:8090"
            value={listen}
            disabled={working}
            onChange={(e) => setListen(e.target.value)}
          />
          <div className="hint">
            Leave it empty for 127.0.0.1:8090. A port alone listens on this machine only, which is all a
            tunnel needs. The hub opens it again each time it starts.
          </div>
        </div>
      )}
      {error && (
        <div className="form-error" role="alert">
          <Marked text={error} />
        </div>
      )}
      <div className="pillar-actions">
        {listener.on ? (
          <>
            {!listener.from_flag && (
              <button className="btn" onClick={() => turn(false)} disabled={working}>
                {working ? 'Turning off…' : 'Turn off'}
              </button>
            )}
            <button className="btn primary" onClick={onNext} disabled={working}>
              Next
            </button>
          </>
        ) : (
          <button className="btn primary" onClick={() => turn(true)} disabled={working}>
            {working ? 'Turning on…' : 'Turn on'}
          </button>
        )}
      </div>
    </>
  )
}

const TUNNELS = [
  { key: 'cloudflare', name: 'Cloudflare Tunnel', why: 'Free. Needs a domain on Cloudflare. Recommended.' },
  { key: 'tailscale', name: 'Tailscale Funnel', why: 'Free. No domain: you get a name under ts.net.' },
  { key: 'proxy', name: 'My own HTTPS proxy', why: 'You already serve HTTPS with nginx, Caddy or similar.' },
] as const
type Tunnel = (typeof TUNNELS)[number]['key']

function hostOf(address: string): string {
  try {
    return new URL(address.trim()).hostname
  } catch {
    return ''
  }
}

function AddressStep({
  view,
  setBusy,
  onBack,
  onNext,
}: {
  view: CloudSettings
  setBusy: (busy: boolean) => void
  onBack: () => void
  onNext: () => void
}) {
  const { run, error } = useCloudChange(setBusy)
  const [address, setAddress] = useState(view.address)
  const [tunnel, setTunnel] = useState<Tunnel>('cloudflare')
  const [working, setWorking] = useState(false)
  const port = listenerPort(view.listener.addr) || '8090'
  const host = hostOf(address) || hostOf(view.address)
  const unchanged = address.trim() === view.address && view.address !== ''
  const check = async () => {
    setWorking(true)
    const next = await run(() => (unchanged ? api.checkCloudAddress() : api.setCloudAddress(address.trim())))
    // the hub stores the address normalized (no trailing slash, host in
    // lower case), and the field takes that form so the result shows
    if (next) setAddress(next.address)
    setWorking(false)
  }

  return (
    <>
      <p className="step-dialog-lede">
        This machine isn't reachable from the internet, so a tunnel gives the listener a public HTTPS address
        and forwards what arrives there to it:
      </p>
      <Route host={host} hub={view.listener.addr ?? `127.0.0.1:${port}`} />
      <div className="cloud-tunnels" role="radiogroup" aria-label="Tunnel">
        {TUNNELS.map((t) => (
          <label key={t.key} className={`cloud-tunnel${t.key === tunnel ? ' on' : ''}`}>
            <input
              type="radio"
              name="cloud-tunnel"
              checked={t.key === tunnel}
              onChange={() => setTunnel(t.key)}
            />
            <span className="cloud-tunnel-name">{t.name}</span>
            <span className="cloud-tunnel-why">{t.why}</span>
          </label>
        ))}
      </div>
      <Recipe tunnel={tunnel} port={port} host={host} />
      <form
        className="egress-add cloud-address"
        onSubmit={(e) => {
          e.preventDefault()
          check()
        }}
      >
        <input
          className="mono"
          aria-label="Public address"
          placeholder="https://spool.example.com"
          value={address}
          disabled={working}
          onChange={(e) => setAddress(e.target.value)}
        />
        <button className="btn primary" disabled={working || !address.trim()}>
          {working ? 'Checking…' : unchanged ? 'Check again' : 'Check'}
        </button>
      </form>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      {unchanged && view.check && (
        <div aria-live="polite">
          <CheckRows view={view} />
        </div>
      )}
      {(isQuickTunnel(address) || (unchanged && view.warning === 'quick_tunnel')) && <QuickTunnelWarning />}
      <p className="cloud-small">
        Use a name you keep: a cloud loop sends its token to whoever answers there.
      </p>
      <div className="pillar-actions">
        <button className="btn" onClick={onBack} disabled={working}>
          Back
        </button>
        <button
          className="btn primary"
          onClick={onNext}
          disabled={working || !unchanged || view.check?.state !== 'ok'}
        >
          Next
        </button>
      </div>
    </>
  )
}

// The cloud setup's last step: the hub's one Claude login (ADR-0058), which
// cloud loops' sessions run under as contained loops' turns do. It is signed
// in here or in Settings → Claude login, and one sign-in serves both.
function AccountStep({
  view,
  setBusy,
  onBack,
  onNext,
}: {
  view: CloudSettings
  setBusy: (busy: boolean) => void
  onBack: () => void
  onNext: () => void
}) {
  const [working, setWorking] = useState(false)
  const signedIn = view.account.set && view.account.check?.state === 'ok'
  const busy = (b: boolean) => {
    setWorking(b)
    setBusy(b)
  }
  return (
    <>
      <p className="step-dialog-lede">
        Cloud loops run on the hub's Claude login, the one contained loops use too. Signed in once, here or in
        Settings → Claude login, it serves both.
      </p>
      {signedIn ? <SignedIn /> : <SignInSteps view={view} setBusy={busy} />}
      <div className="pillar-actions">
        <button className="btn" onClick={onBack} disabled={working}>
          Back
        </button>
        <button className="btn primary" onClick={onNext} disabled={working || !signedIn}>
          Next
        </button>
      </div>
    </>
  )
}

// The environment step (ADR-0059), in OPERATIONS.md's order: make
// the environment on claude.ai, paste the list into its network access,
// pick it here, confirm. The hub builds the list on every read of the page,
// and the confirm names its digest, so a list changed since is refused.
function EnvironmentStep({
  view,
  setBusy,
  onBack,
  onNext,
}: {
  view: CloudSettings
  setBusy: (busy: boolean) => void
  onBack: () => void
  onNext: () => void
}) {
  const { run, error } = useCloudChange(setBusy)
  const { environment } = view

  return (
    <>
      <p className="step-dialog-lede">
        Every cloud loop runs in one environment you make on claude.ai. It holds network settings only. Every
        cloud loop shares it, so put no variables or secrets in it: a loop's secrets come from this hub.
      </p>
      <ol className="cloud-recipe">
        <li>
          On{' '}
          <a href="https://claude.ai/code" target="_blank" rel="noreferrer">
            claude.ai/code
          </a>
          , open the environment selector under the message box and choose <strong>New environment</strong>.
          Name it <Copyable text="spool" />.
        </li>
        <li>
          Under <strong>Network access</strong>, choose <strong>Custom</strong> and paste this list into{' '}
          <strong>Allowed domains</strong>. Leave{' '}
          <strong>Also include default list of common package managers</strong> unchecked, then choose{' '}
          <strong>Add environment</strong>.
          <Copyable text={environment.allowlist} label="the allowed domains" />
          {environment.not_applied.length > 0 && (
            <div className="cloud-small">
              Left out, as an environment can't take a port or a bare <code>*</code>:{' '}
              {environment.not_applied.map((entry, i) => (
                <span key={entry}>
                  {i > 0 && ', '}
                  <code>{entry}</code>
                </span>
              ))}
              .
            </div>
          )}
        </li>
        <li>
          Pick it here.
          <EnvironmentPick environment={environment} run={run} />
        </li>
        <li>
          Confirm that you pasted the list from step 2 into the environment's settings on claude.ai. If the
          allowlist in Settings → Egress, the public address or the environment changes, the hub asks you to
          paste it again.
          {environment.confirmed ? (
            <p className="step-dialog-state" aria-live="polite">
              <span className="ok">
                <CheckIcon size={14} /> {environment.name || environment.id} holds this hub's allowlist.
              </span>
            </p>
          ) : (
            <div className="cloud-code">
              <button
                className="btn"
                disabled={!environment.id}
                onClick={() => void run(() => api.confirmCloudEnvironment(environment.digest))}
              >
                I pasted it
              </button>
            </div>
          )}
        </li>
      </ol>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="pillar-actions">
        <button className="btn" onClick={onBack}>
          Back
        </button>
        <button className="btn primary" onClick={onNext} disabled={!environment.confirmed}>
          Next
        </button>
      </div>
    </>
  )
}

// The environment, picked by name from the account's list, which the hub
// reads on its Claude login (ADR-0059). That list rests on an API
// Anthropic doesn't publish, so when it fails the step falls back to the
// documented way to an ID: /remote-env in the operator's own claude.
function EnvironmentPick({
  environment,
  run,
}: {
  environment: CloudSettings['environment']
  run: (request: () => Promise<CloudSettings>) => Promise<CloudSettings | undefined>
}) {
  const {
    data,
    error: listError,
    refetch,
    isFetching,
  } = useQuery({
    queryKey: ['cloud', 'environments'],
    queryFn: api.cloudEnvironments,
    refetchOnMount: 'always',
    retry: false,
  })
  const [pasted, setPasted] = useState('')

  if (listError) {
    const typed = pasted.trim()
    return (
      <>
        <div className="cloud-small">
          The hub couldn't list your environments (
          {listError instanceof Error ? listError.message : String(listError)}). Pick it in your own Claude
          Code instead: run <Copyable text="/remote-env" /> in <code>claude</code>, choose the environment,
          then copy <code>remote.defaultEnvironmentId</code> from <code>~/.claude/settings.json</code> here.
        </div>
        <form
          className="egress-add cloud-code"
          onSubmit={(e) => {
            e.preventDefault()
            void run(() => api.setCloudEnvironment(typed))
          }}
        >
          <input
            className="mono"
            value={pasted}
            onChange={(e) => setPasted(e.target.value)}
            placeholder={environment.id || 'env_…'}
            aria-label="Environment ID"
            spellCheck={false}
          />
          <button className="btn" disabled={!typed || typed === environment.id}>
            Save
          </button>
        </form>
      </>
    )
  }
  if (!data) return null
  const options = environmentOptions(data.environments, environment)
  return (
    <div className="egress-add cloud-code">
      {data.environments.length === 0 && !environment.id ? (
        <span className="cloud-small">No environments on this account yet.</span>
      ) : (
        <select
          value={environment.id}
          onChange={(e) => void run(() => api.setCloudEnvironment(e.target.value))}
          aria-label="Environment"
        >
          {options.map((option) => (
            <option key={option.value} value={option.value} disabled={option.value === ''}>
              {option.label}
            </option>
          ))}
        </select>
      )}
      <button
        type="button"
        className="panel-action"
        onClick={() => void refetch()}
        disabled={isFetching}
        aria-label="Refresh the list"
        title="Refresh the list"
      >
        <RefreshIcon />
      </button>
    </div>
  )
}

// Who calls whom: a cloud loop calls the public address, and the tunnel
// forwards it to this hub's listener, and to nothing else of the hub's.
function Route({ host, hub }: { host: string; hub: string }) {
  return (
    <div className="cloud-route" aria-label="How a cloud loop reaches this hub">
      <div className="cloud-node">
        <div className="cloud-node-k">cloud loop</div>
        <div className="cloud-node-v">Anthropic's machine</div>
      </div>
      <div className="cloud-arrow">
        <span className="cloud-arrow-label">HTTPS</span>
      </div>
      <div className="cloud-node public">
        <div className="cloud-node-k">public address</div>
        <div className="cloud-node-v mono" title={host || undefined}>
          {host || 'your address'}
        </div>
        <div className="cloud-node-sub">your tunnel</div>
      </div>
      <div className="cloud-arrow" />
      <div className="cloud-node">
        <div className="cloud-node-k">this hub</div>
        <div className="cloud-node-v mono">{hub}</div>
        <div className="cloud-node-sub">cloud listener only</div>
      </div>
    </div>
  )
}

function Recipe({ tunnel, port, host }: { tunnel: Tunnel; port: string; host: string }) {
  // a quick tunnel's name is no name to route a named tunnel to
  const name = host && !isQuickTunnelHost(host) ? host : 'spool.example.com'
  if (tunnel === 'tailscale')
    return (
      <ol className="cloud-recipe">
        <li>
          Install Tailscale on this machine and sign in. Funnel has to be allowed in your tailnet, which the
          admin console offers the first time you use it.
        </li>
        <li>
          Run <Copyable text={`tailscale funnel --bg ${port}`} />. It keeps running after you close the
          terminal.
        </li>
        <li>
          Enter the address it prints, such as <code>https://hub.tail1234.ts.net</code>, below.
        </li>
        <li className="cloud-recipe-aside">
          A proxy such as Caddy already holds :443 on this machine? The funnel works, but the hub's check from
          here reaches the proxy instead.{' '}
          <a href={FUNNEL_BESIDE_PROXY_DOCS} target="_blank" rel="noreferrer">
            Give the proxy the name too
          </a>
          .
        </li>
      </ol>
    )
  if (tunnel === 'proxy')
    return (
      <ol className="cloud-recipe">
        <li>
          Point a name you own at your proxy, with a valid certificate. Plain HTTP is refused: the loop's
          token travels in the request.
        </li>
        <li>
          Forward that name to <code>localhost:{port}</code> and nothing else. The control room's port stays
          off the internet.
        </li>
        <li>Enter the address below.</li>
      </ol>
    )
  return (
    <ol className="cloud-recipe">
      <li>
        Install <code>cloudflared</code> and sign in: <Copyable text="cloudflared tunnel login" />
      </li>
      <li>
        Create the tunnel once: <Copyable text="cloudflared tunnel create spool" />
      </li>
      <li>
        Give it a name on your domain: <Copyable text={`cloudflared tunnel route dns spool ${name}`} />
      </li>
      <li>
        Run it: <Copyable text={`cloudflared tunnel run --url http://localhost:${port} spool`} />
      </li>
      <li className="cloud-recipe-aside">
        No domain on Cloudflare? A quick tunnel works too:{' '}
        <Copyable text={`cloudflared tunnel --url http://localhost:${port}`} />. Its name changes each time it
        starts.
      </li>
    </ol>
  )
}
