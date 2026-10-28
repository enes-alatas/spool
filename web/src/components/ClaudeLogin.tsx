import { useState } from 'react'
import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api, ApiError, type ClaudeLogin, type CloudSettings, type Settings } from '../api'
import { accountState, loginState, signInCodeShaped } from '../cloud'
import {
  checking,
  defaultFirst,
  defaultLineState,
  loginLineState,
  loginNameError,
  loginWho,
  loopsOn,
  mayManageLogin,
  signOutConfirm,
} from '../claudeLogins'
import { formatAgo } from '../format'
import { CheckIcon } from './Icons'
import { Copyable } from './Copyable'
import { StepDialog } from './StepDialog'
import { useMinuteClock } from './PlanUsage'
import { useMay } from './Session'

// The hub's Claude logins (ADR-0058, ADR-0060): the claude.ai accounts the
// hub signs in to with the CLI's own login. Contained loops reach the model
// through the hub, which adds a login's token, and cloud loops' sessions
// run under the default. Settings, first run and the cloud setup each sign
// the default in, and Settings → Claude logins lists every login, adds one
// and signs in, checks, signs out and removes each.

// A change of the default login is the cloud account's too (ADR-0060),
// which Settings → Cloud, first run and the default's line read, and the
// hub checks it again; the per-login routes answer with the login, so the
// account is read afresh.
function cloudChanged(qc: QueryClient) {
  void qc.invalidateQueries({ queryKey: ['cloud'] })
}

// Every change to a login can change the hub's Claude token, which the
// settings, first run and the list each read.
function loginsChanged(qc: QueryClient) {
  void qc.invalidateQueries({ queryKey: ['claude-logins'] })
  void qc.invalidateQueries({ queryKey: ['settings'] })
  void qc.invalidateQueries({ queryKey: ['onboarding'] })
}

// Settings → Claude logins: a line a login, and Add. Any user sees the
// list and adds a login of their own; a login's owner or an admin acts on
// it (ADR-0060). The default's line, for an admin, is the one the
// hub has always had: it reads Settings → Cloud's account, whose check says
// why a sign-in failed, together with the hub's login check.
export function ClaudeLoginSection({ settings }: { settings?: Settings }) {
  const { data: view } = useQuery({ queryKey: ['cloud'], queryFn: api.cloudSettings })
  const { data: me } = useQuery({ queryKey: ['session'], queryFn: api.me, retry: false })
  const { data: list, error: loadError } = useQuery({
    queryKey: ['claude-logins'],
    queryFn: api.claudeLogins,
    refetchInterval: (q) => (checking(q.state.data?.logins) ? 2000 : false),
  })
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const may = useMay()
  const [signingIn, setSigningIn] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const logins = list?.logins

  return (
    <section className="claude-login">
      <h2 className="section-head" id="claude-login">
        Claude logins
      </h2>
      <p className="page-lede">
        A Claude login is a claude.ai account this hub signs in to. Contained loops reach the model through
        the hub, which adds the login, and cloud loops' sessions run under it. The hub keeps each login
        sealed, renews it before it runs out, and never shows it. A contained loop runs on the login it names,
        or on default when it names none, and a cloud loop on default. Bare loops keep this machine's own
        login.
      </p>
      {loadError ? (
        <div className="form-error" role="alert">
          Could not load the Claude logins:{' '}
          {loadError instanceof Error ? loadError.message : String(loadError)}
        </div>
      ) : !logins ? null : (
        <div className="cloud-steps">
          {defaultFirst(logins).map((login) =>
            login.default && may('manage') && view ? (
              <DefaultLogin
                key={login.name}
                login={login}
                held={loopsOn(loops, login.name)}
                view={view}
                settings={settings}
                onSignIn={() => setSigningIn(login.name)}
              />
            ) : (
              <NamedLogin
                key={login.name}
                login={login}
                held={loopsOn(loops, login.name)}
                manages={mayManageLogin(me, login)}
                cli={view?.account.cli ?? true}
                onSignIn={() => setSigningIn(login.name)}
              />
            ),
          )}
        </div>
      )}
      {logins && (
        <div className="cloud-actions">
          <button className="btn sm" onClick={() => setAdding(true)}>
            Add a Claude login
          </button>
        </div>
      )}
      {signingIn === 'default' && <SignInDialog onClose={() => setSigningIn(null)} />}
      {signingIn && signingIn !== 'default' && (
        <LoginSignInDialog name={signingIn} onClose={() => setSigningIn(null)} />
      )}
      {adding && logins && <AddLoginDialog logins={logins} onClose={() => setAdding(false)} />}
    </section>
  )
}

// The default login's line for an admin: its state as Settings → Cloud's
// account check and the hub's login check (ADR-0044) read it, with why a
// check failed under it. One Check again runs both (#1174): the account check tries
// the login as a cloud loop's session would, the login check as this hub's
// loops run, and an operator needs only to know whether it works.
function DefaultLogin({
  login,
  held,
  view,
  settings,
  onSignIn,
}: {
  login: ClaudeLogin
  held: string[]
  view: CloudSettings
  settings?: Settings
  onSignIn: () => void
}) {
  const [working, setWorking] = useState(false)
  const [error, setError] = useState('')
  const qc = useQueryClient()
  const now = useMinuteClock()

  const change = async (request: () => Promise<unknown>) => {
    setWorking(true)
    setError('')
    try {
      await request()
      cloudChanged(qc)
      loginsChanged(qc)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setWorking(false)
    }
  }
  const signOut = () => {
    if (confirm(signOutConfirm(login.name, held))) void change(() => api.signOutClaudeLogin('default'))
  }

  return (
    <>
      <LoginLine
        login={login}
        view={view}
        now={now}
        working={working}
        onSignIn={onSignIn}
        onCheck={() => change(checkDefault)}
        onSignOut={signOut}
      />
      <LoginNote login={login} view={view} settings={settings} error={error} />
    </>
  )
}

// Both checks of the default in one request (#1174): the hub checks the
// cloud account before it answers, and starts its login check, which runs
// in the background. A hub with nothing to run the login check with
// refuses it 409 after the account check, which then speaks alone.
async function checkDefault() {
  try {
    await api.checkClaudeLogin('default')
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 409)) throw e
  }
}

function LoginLine({
  login,
  view,
  now,
  working,
  onSignIn,
  onCheck,
  onSignOut,
}: {
  login: ClaudeLogin
  view: CloudSettings
  now: number
  working: boolean
  onSignIn: () => void
  onCheck: () => void
  onSignOut: () => void
}) {
  const { set, cli, check } = view.account
  const state = set ? defaultLineState(loginState(view), login) : loginState(view)
  const pending = login.check?.status === 'pending'
  const checkedAt = Math.max(check?.checked_at ?? 0, pending ? 0 : (login.check?.at ?? 0))
  return (
    <div className={`cloud-step ${state.tone}`}>
      <span className="cloud-dot" aria-hidden />
      <LoginName login={login} />
      <span className="cloud-step-end">
        <span className="cloud-step-state">
          {state.text}
          {set && !pending && checkedAt > 0 && ` · checked ${formatAgo(checkedAt, now)}`}
        </span>
        {set ? (
          <>
            {check?.stage === 'refused' || login.check?.status === 'refused' ? (
              cli && (
                <button className="btn sm" onClick={onSignIn} disabled={working}>
                  Sign in again
                </button>
              )
            ) : (
              <button className="btn sm" onClick={onCheck} disabled={working || pending}>
                {working || pending ? 'Checking…' : 'Check again'}
              </button>
            )}
            <button
              className="btn sm"
              aria-label="Sign out of the Claude login default"
              onClick={onSignOut}
              disabled={working}
            >
              Sign out
            </button>
          </>
        ) : (
          cli && (
            <button className="btn sm primary" onClick={onSignIn}>
              Sign in with Claude
            </button>
          )
        )}
      </span>
    </div>
  )
}

// A login's name, and who it is: the address it signed in with, when the
// session may see it, and whose it is.
function LoginName({ login }: { login: ClaudeLogin }) {
  const who = loginWho(login)
  return (
    <>
      <span className="cloud-step-title mono">{login.name}</span>
      {who && <span className="claude-login-who">{who}</span>}
    </>
  )
}

// Any login but the default as an admin sees it, and the default as a
// member does: its state from its own check, and, for its owner or an
// admin, its actions. Sign out is never refused (ADR-0060); Remove
// is refused while loops run on the login, and the refusal names them.
function NamedLogin({
  login,
  held,
  manages,
  cli,
  onSignIn,
}: {
  login: ClaudeLogin
  held: string[]
  manages: boolean
  cli: boolean
  onSignIn: () => void
}) {
  const [working, setWorking] = useState<'' | 'check' | 'sign-out' | 'remove'>('')
  const [error, setError] = useState('')
  const qc = useQueryClient()
  const now = useMinuteClock()
  const state = loginLineState(login)
  const { check } = login

  const act = async (what: 'check' | 'sign-out' | 'remove', request: () => Promise<unknown>) => {
    setWorking(what)
    setError('')
    try {
      await request()
      loginsChanged(qc)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setWorking('')
    }
  }
  const signOut = () => {
    if (confirm(signOutConfirm(login.name, held)))
      void act('sign-out', () => api.signOutClaudeLogin(login.name))
  }
  const remove = () => {
    if (confirm(`Remove the Claude login ${login.name}? The hub signs it out first.`))
      void act('remove', () => api.removeClaudeLogin(login.name))
  }

  return (
    <>
      <div className={`cloud-step ${state.tone}`}>
        <span className="cloud-dot" aria-hidden />
        <LoginName login={login} />
        <span className="cloud-step-end">
          <span className="cloud-step-state">
            {state.text}
            {login.signed_in &&
              check &&
              check.status !== 'pending' &&
              ` · checked ${formatAgo(check.at, now)}`}
          </span>
          {manages && (
            <>
              {!login.signed_in || check?.status === 'refused' ? (
                cli && (
                  <button
                    className={`btn sm${login.signed_in ? '' : ' primary'}`}
                    onClick={onSignIn}
                    disabled={working !== ''}
                  >
                    {login.signed_in ? 'Sign in again' : 'Sign in'}
                  </button>
                )
              ) : (
                <button
                  className="btn sm"
                  onClick={() => act('check', () => api.checkClaudeLogin(login.name))}
                  disabled={working !== '' || check?.status === 'pending'}
                >
                  {working === 'check' || check?.status === 'pending' ? 'Checking…' : 'Check again'}
                </button>
              )}
              {login.signed_in && (
                <button
                  className="btn sm"
                  aria-label={`Sign out of the Claude login ${login.name}`}
                  onClick={signOut}
                  disabled={working !== ''}
                >
                  Sign out
                </button>
              )}
              {!login.default && (
                <button
                  className="btn sm danger"
                  aria-label={`Remove the Claude login ${login.name}`}
                  onClick={remove}
                  disabled={working !== ''}
                >
                  Remove
                </button>
              )}
            </>
          )}
        </span>
      </div>
      {(error || (login.signed_in && check?.status === 'refused' && check.refusal)) && (
        <div className="cloud-step-open">
          {login.signed_in && check?.status === 'refused' && (
            <div className="cloud-check-hint">
              {check.refusal ? `${check.refusal} ` : ''}Loops can't run on a refused login. Sign in again.
            </div>
          )}
          {error && (
            <div className="form-error" role="alert">
              {error}
            </div>
          )}
        </div>
      )}
    </>
  )
}

// Whose subscription a login is (ADR-0060), said where one is added.
const TERMS =
  'A Claude login is the subscription of the person who signs it in, and runs the loops they run. ' +
  'Spool never pools or resells capacity. You are responsible for following the terms of your plan.'

// Add a Claude login: a name, then the same sign-in the default has. The
// login is added signed out, owned by whoever adds it, so a sign-in left
// unfinished leaves it on the list to sign in later.
function AddLoginDialog({ logins, onClose }: { logins: ClaudeLogin[]; onClose: () => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [added, setAdded] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const invalid = loginNameError(name, logins)

  const add = async () => {
    if (invalid || !name.trim()) return
    setBusy(true)
    setError('')
    try {
      const login = await api.addClaudeLogin(name.trim())
      void qc.invalidateQueries({ queryKey: ['claude-logins'] })
      setAdded(login.name)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (added) return <LoginSignInDialog name={added} onClose={onClose} />
  return (
    <StepDialog title="Add a Claude login" busy={busy} onClose={onClose}>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault()
          void add()
        }}
      >
        <p className="step-dialog-lede">{TERMS}</p>
        <div className="field">
          <label htmlFor="add-claude-login">Name</label>
          <input
            id="add-claude-login"
            className="mono"
            value={name}
            autoFocus
            autoComplete="off"
            spellCheck={false}
            placeholder="ada"
            disabled={busy}
            onChange={(e) => setName(e.target.value)}
          />
          <div className={`hint${invalid ? ' warn' : ''}`}>
            {invalid ||
              'Lowercase, 2–32 characters. You sign it in next, and it is yours to sign out or remove.'}
          </div>
        </div>
        {error && (
          <div className="form-error" role="alert">
            {error}
          </div>
        )}
        <div className="pillar-actions">
          <button className="btn primary" disabled={busy || !name.trim() || invalid !== ''}>
            {busy ? 'Adding…' : 'Add and sign in'}
          </button>
        </div>
      </form>
    </StepDialog>
  )
}

// Signing a login other than the default in, from its line or straight
// after Add. It closes on Done once the hub has checked the login.
function LoginSignInDialog({ name, onClose }: { name: string; onClose: () => void }) {
  const { data: view } = useQuery({ queryKey: ['cloud'], queryFn: api.cloudSettings })
  const { data: list } = useQuery({
    queryKey: ['claude-logins'],
    queryFn: api.claudeLogins,
    refetchInterval: (q) => (checking(q.state.data?.logins) ? 2000 : false),
  })
  const [busy, setBusy] = useState(false)
  const login = list?.logins.find((l) => l.name === name)
  const signedIn = login?.signed_in === true && login.check?.status === 'ok'
  return (
    <StepDialog title={`Sign in ${name}`} busy={busy} onClose={onClose}>
      <div className="form">
        <p className="step-dialog-lede">
          The hub signs in to your Claude account under the name {name}. It keeps the login sealed, renews it,
          and never shows it. You can sign out in Settings → Claude logins.
        </p>
        {view &&
          login &&
          (signedIn ? <SignedIn /> : <SignInSteps view={view} login={login} setBusy={setBusy} />)}
        <form method="dialog" className="pillar-actions">
          <button className={`btn${signedIn ? ' primary' : ''}`} disabled={busy}>
            {signedIn ? 'Done' : 'Close'}
          </button>
        </form>
      </div>
    </StepDialog>
  )
}

// What the line can't say in a few words: why a check failed, what a
// refused login stops, how a hub with no claude CLI to run gets one, and that a
// setup-token saved before the login still runs contained loops until one
// is signed in (ADR-0058's migration).
function LoginNote({
  login,
  view,
  settings,
  error,
}: {
  login: ClaudeLogin
  view: CloudSettings
  settings?: Settings
  error: string
}) {
  const { set, cli } = view.account
  const { tone } = accountState(view)
  // the hub's login check says only that it was refused, and what the API
  // answered, so its refusal stands where the account check gives none
  const checkRefused = set && login.check?.status === 'refused'
  const refused = (set && view.account.check?.stage === 'refused') || checkRefused
  const setupToken = !set && settings?.claude_token_set === true
  const lines = [
    set && tone === 'bad' && <AccountHint key="hint" view={view} />,
    checkRefused && tone !== 'bad' && login.check?.refusal && (
      <div className="cloud-check-hint" key="refusal">
        {login.check.refusal}
      </div>
    ),
    refused && (
      <div className="cloud-check-hint" key="held">
        Loops can't run on a refused login: contained loops stop at their next turn, and cloud loops' sessions
        are refused.{cli && ' Sign in again to start them.'}
      </div>
    ),
    (!set || refused) && !cli && (
      <div className="cloud-check-hint" key="cli">
        <NoClaudeCLI again={refused} />
      </div>
    ),
    !set && (
      <div className="cloud-check-hint" key="none">
        {setupToken
          ? 'Contained loops run on the setup-token saved before this hub had a Claude login. Signing in replaces it.'
          : 'Until the hub is signed in, contained and cloud loops have no Claude login to run on.'}
      </div>
    ),
    error && (
      <div className="form-error" role="alert" key="error">
        {error}
      </div>
    ),
  ].filter(Boolean)
  return lines.length > 0 ? <div className="cloud-step-open">{lines}</div> : null
}

// What a hub with no sign-in to run says (#955): the hub runs the claude
// CLI in the workstation image, or the host's own when it has no image
// (#952), so `cli` is false only with neither, and either one fixes it. On
// the standard deployment the hub runs as its own system user, which can't
// reach the operator's install, so the image is named first.
function NoClaudeCLI({ again = false }: { again?: boolean }) {
  return (
    <>
      The sign-in runs the <code>claude</code> CLI, and the hub finds neither the workstation image nor Claude
      Code on this machine. Fetch the image with <Copyable text="spool images | sh" />, or{' '}
      <Copyable text="make image" /> in a source checkout, or install Claude Code where the hub's user can run
      it. Then reload this page to sign in{again && ' again'}.
    </>
  )
}

// Why the last check failed, and the hub's detail of it (an admin's alone).
export function AccountHint({ view }: { view: CloudSettings }) {
  const { hint } = accountState(view)
  const detail = view.account.check?.detail
  return (
    <>
      {hint && <div className="cloud-check-hint">{hint}</div>}
      {detail && <div className="cloud-check-detail">{detail}</div>}
    </>
  )
}

// Signing in, over whichever page asked: first run's Harness card and
// Settings → Claude logins. It closes on Done once the hub has checked the
// login.
export function SignInDialog({ onClose }: { onClose: () => void }) {
  const { data: view } = useQuery({ queryKey: ['cloud'], queryFn: api.cloudSettings })
  const [busy, setBusy] = useState(false)
  const signedIn = view?.account.set === true && view.account.check?.state === 'ok'
  return (
    <StepDialog title="Sign in with Claude" busy={busy} onClose={onClose}>
      <div className="form">
        <p className="step-dialog-lede">
          Contained and cloud loops run on the hub's default Claude login, so the hub signs in to your Claude
          account once. It keeps the login sealed, renews it, and never shows it. You can sign out in Settings
          → Claude logins.
        </p>
        {view && (signedIn ? <SignedIn /> : <SignInSteps view={view} setBusy={setBusy} />)}
        <form method="dialog" className="pillar-actions">
          <button className={`btn${signedIn ? ' primary' : ''}`} disabled={busy}>
            {signedIn ? 'Done' : 'Close'}
          </button>
        </form>
      </div>
    </StepDialog>
  )
}

export function SignedIn() {
  return (
    <p className="step-dialog-state" aria-live="polite">
      <span className="ok">
        <CheckIcon size={14} /> Signed in, and Anthropic's API accepts the login.
      </span>
    </p>
  )
}

// The sign-in itself (#864): the hub runs the CLI's own login, which prints a
// claude.ai link; claude.ai then shows a code, and the hub hands it back to
// the login. A refused code ends that sign-in, as ten minutes without one
// does, so either starts again from the link. A login already signed in
// whose check failed says so above the steps, so signing in again replaces
// it. With no claude CLI to run, neither the workstation image's nor this
// machine's, there is no sign-in, and the steps say how to get one. Given a
// login, it signs that one in; without one, the default. Both go through
// the login's own routes (#1173).
export function SignInSteps({
  view,
  login,
  setBusy,
}: {
  view: CloudSettings
  login?: ClaudeLogin
  setBusy: (busy: boolean) => void
}) {
  const qc = useQueryClient()
  const [link, setLink] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [working, setWorking] = useState<'' | 'start' | 'code' | 'check'>('')
  const { cli } = view.account
  const set = login ? login.signed_in : view.account.set
  const checked = login ? login.check !== undefined : view.account.check !== undefined

  const act = async (what: 'start' | 'code' | 'check', request: () => Promise<void>) => {
    setWorking(what)
    setError('')
    setBusy(true)
    try {
      await request()
    } catch (e) {
      // A refused code, or none waiting, has ended the sign-in.
      if (e instanceof ApiError && e.code === 'cloud_sign_in' && what === 'code') {
        setLink('')
        setCode('')
        setError(
          e.status === 400
            ? "claude.ai's code wasn't accepted, and that sign-in has ended. Sign in again."
            : 'That sign-in has ended: it waits ten minutes for its code, and a new one ends it. Sign in again.',
        )
      } else {
        setError(e instanceof Error ? e.message : String(e))
      }
    } finally {
      setBusy(false)
      setWorking('')
    }
  }
  const start = () => {
    // The tab opens on the click, before the link is known, so no popup
    // blocker takes it; it is pointed at the link once the hub answers.
    const tab = window.open('', '_blank')
    if (tab) tab.opener = null
    return act('start', async () => {
      try {
        const { link } = await api.startClaudeLoginSignIn(login?.name ?? 'default')
        setLink(link)
        setCode('')
        if (tab) tab.location.href = link
      } catch (e) {
        tab?.close()
        throw e
      }
    })
  }
  const finish = () => {
    if (!signInCodeShaped(code)) {
      setError("That isn't a sign-in code: paste it as claude.ai shows it, one word with no spaces.")
      return
    }
    return act('code', async () => {
      await api.finishClaudeLoginSignIn(login?.name ?? 'default', code.trim())
      if (!login) cloudChanged(qc)
      loginsChanged(qc)
      setLink('')
      setCode('')
    })
  }
  const recheck = () =>
    act('check', async () => {
      if (login) await api.checkClaudeLogin(login.name)
      else {
        await checkDefault()
        cloudChanged(qc)
      }
      void qc.invalidateQueries({ queryKey: ['claude-logins'] })
    })

  return (
    <>
      {set && (
        <div aria-live="polite">
          <p className="step-dialog-state">
            {login?.check?.status === 'pending'
              ? 'Signed in, checking the login…'
              : checked
                ? 'Signed in, but the last check failed.'
                : 'Signed in, not checked yet.'}
          </p>
          {login ? (
            login.check?.refusal && <div className="cloud-check-hint">{login.check.refusal}</div>
          ) : (
            <AccountHint view={view} />
          )}
        </div>
      )}
      {cli ? (
        <ol className="cloud-recipe">
          <li>
            <button className="btn primary" onClick={start} disabled={working !== ''}>
              {working === 'start' ? 'Starting…' : link ? 'Start again' : 'Sign in with Claude ↗'}
            </button>
            <div className="cloud-small">
              {link ? (
                <>
                  claude.ai opened in a new tab. If it didn't,{' '}
                  <a href={link} target="_blank" rel="noopener noreferrer">
                    open the sign-in
                  </a>
                  .
                </>
              ) : (
                'claude.ai opens in a new tab and asks you to allow the sign-in.'
              )}
            </div>
          </li>
          <li>
            Paste the code claude.ai shows you:
            <form
              className="egress-add cloud-code"
              onSubmit={(e) => {
                e.preventDefault()
                finish()
              }}
            >
              <input
                className="mono"
                aria-label="Code"
                placeholder="code"
                autoComplete="off"
                spellCheck={false}
                value={code}
                disabled={!link || working !== ''}
                onChange={(e) => setCode(e.target.value)}
              />
              <button className="btn primary" disabled={!link || !code.trim() || working !== ''}>
                {working === 'code' ? 'Signing in…' : 'Sign in'}
              </button>
            </form>
          </li>
        </ol>
      ) : (
        <p className="step-dialog-state">
          <NoClaudeCLI />
        </p>
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      {set && (
        <div className="pillar-actions">
          <button className="btn" onClick={recheck} disabled={working !== ''}>
            {working === 'check' ? 'Checking…' : 'Check again'}
          </button>
        </div>
      )}
    </>
  )
}
