import { useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, type CloudSettings, type Settings } from '../api'
import { accountState, loginState, signInCodeShaped } from '../cloud'
import { formatAgo } from '../format'
import { CheckIcon } from './Icons'
import { Copyable } from './Copyable'
import { StepDialog } from './StepDialog'
import { useMinuteClock } from './PlanUsage'

// The hub's one Claude login (ADR-0058, #929): the claude.ai account the hub
// signs in to with the CLI's own login. Contained loops reach the model
// through the hub, which adds the login's token, and cloud loops' sessions
// run under it. Settings, first run and the cloud setup each sign in here,
// and Settings → Claude login is where it is checked and signed out.

// Settings → Claude login: the login's state, its check, and Sign out.
// Signing in opens the same dialog first run does. Settings puts the login
// check's line under it as children.
export function ClaudeLoginSection({ settings, children }: { settings?: Settings; children?: ReactNode }) {
  const { data: view, error: loadError } = useQuery({ queryKey: ['cloud'], queryFn: api.cloudSettings })
  const [signingIn, setSigningIn] = useState(false)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState('')
  const qc = useQueryClient()
  const now = useMinuteClock()

  const change = async (request: () => Promise<CloudSettings>) => {
    setWorking(true)
    setError('')
    try {
      qc.setQueryData(['cloud'], await request())
      // the hub's Claude token changed with it
      void qc.invalidateQueries({ queryKey: ['settings'] })
      void qc.invalidateQueries({ queryKey: ['onboarding'] })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setWorking(false)
    }
  }
  const signOut = () => {
    if (confirm(SIGN_OUT)) void change(api.removeCloudAccount)
  }

  return (
    <section className="claude-login">
      <h2 className="section-head" id="claude-login">
        Claude login
      </h2>
      <p className="page-lede">
        Contained and cloud loops run on one Claude login: the claude.ai account this hub signs in to.
        Contained loops reach the model through the hub, which adds the login, and cloud loops' sessions run
        under it. The hub keeps the login sealed, renews it before it runs out, and never shows it. Bare loops
        keep this machine's own login.
      </p>
      {loadError ? (
        <div className="form-error" role="alert">
          Could not load the Claude login:{' '}
          {loadError instanceof Error ? loadError.message : String(loadError)}
        </div>
      ) : !view ? null : (
        <div className="cloud-steps">
          <LoginLine
            view={view}
            now={now}
            working={working}
            onSignIn={() => setSigningIn(true)}
            onCheck={() => change(api.checkCloudAccount)}
            onSignOut={signOut}
          />
          <LoginNote view={view} settings={settings} error={error} />
        </div>
      )}
      {children}
      {signingIn && <SignInDialog onClose={() => setSigningIn(false)} />}
    </section>
  )
}

function LoginLine({
  view,
  now,
  working,
  onSignIn,
  onCheck,
  onSignOut,
}: {
  view: CloudSettings
  now: number
  working: boolean
  onSignIn: () => void
  onCheck: () => void
  onSignOut: () => void
}) {
  const { set, cli, check } = view.account
  const state = loginState(view)
  return (
    <div className={`cloud-step ${state.tone}`}>
      <span className="cloud-dot" aria-hidden />
      <span className="cloud-step-title">Claude login</span>
      <span className="cloud-step-end">
        <span className="cloud-step-state">
          {state.text}
          {set && check && ` · checked ${formatAgo(check.checked_at, now)}`}
        </span>
        {set ? (
          <>
            {check?.stage === 'refused' ? (
              cli && (
                <button className="btn sm" onClick={onSignIn} disabled={working}>
                  Sign in again
                </button>
              )
            ) : (
              <button className="btn sm" onClick={onCheck} disabled={working}>
                {working ? 'Checking…' : 'Check again'}
              </button>
            )}
            <button
              className="btn sm"
              aria-label="Sign out of the Claude login"
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

// What the line can't say in a few words: why a check failed, what a
// refused login stops, how a hub with no claude CLI to run gets one, and that a
// setup-token saved before the login still runs contained loops until one
// is signed in (ADR-0058's migration).
function LoginNote({ view, settings, error }: { view: CloudSettings; settings?: Settings; error: string }) {
  const { set, cli } = view.account
  const { tone } = accountState(view)
  const refused = set && view.account.check?.stage === 'refused'
  const setupToken = !set && settings?.claude_token_set === true
  const lines = [
    set && tone === 'bad' && <AccountHint key="hint" view={view} />,
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

// Sign out asks Anthropic to revoke the login through the CLI's own logout,
// which can't confirm the revoke took (ADR-0058, probe E on #928), so
// claude.ai's list of sign-ins is where the operator checks it ended.
const SIGN_OUT =
  'Sign out of the Claude login? Every contained and cloud loop stops until the hub is signed in again. ' +
  "The hub asks Anthropic to revoke the login but can't confirm it did. claude.ai's settings list the " +
  "account's sign-ins, so you can check there."

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
// Settings → Claude login. It closes on Done once the hub has checked the
// login.
export function SignInDialog({ onClose }: { onClose: () => void }) {
  const { data: view } = useQuery({ queryKey: ['cloud'], queryFn: api.cloudSettings })
  const [busy, setBusy] = useState(false)
  const signedIn = view?.account.set === true && view.account.check?.state === 'ok'
  return (
    <StepDialog title="Sign in with Claude" busy={busy} onClose={onClose}>
      <div className="form">
        <p className="step-dialog-lede">
          Contained and cloud loops run on one Claude login, so the hub signs in to your Claude account once.
          It keeps the login sealed, renews it, and never shows it. You can sign out in Settings → Claude
          login.
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
// machine's, there is no sign-in, and the steps say how to get one.
export function SignInSteps({ view, setBusy }: { view: CloudSettings; setBusy: (busy: boolean) => void }) {
  const qc = useQueryClient()
  const [link, setLink] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [working, setWorking] = useState<'' | 'start' | 'code' | 'check'>('')
  const { set, cli, check } = view.account

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
        const { link } = await api.startCloudSignIn()
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
      qc.setQueryData(['cloud'], await api.finishCloudSignIn(code.trim()))
      // the hub's Claude token changed with it
      void qc.invalidateQueries({ queryKey: ['settings'] })
      void qc.invalidateQueries({ queryKey: ['onboarding'] })
      setLink('')
      setCode('')
    })
  }
  const recheck = () =>
    act('check', async () => {
      qc.setQueryData(['cloud'], await api.checkCloudAccount())
    })

  return (
    <>
      {set && (
        <div aria-live="polite">
          <p className="step-dialog-state">
            {check ? 'Signed in, but the last check failed.' : 'Signed in, not checked yet.'}
          </p>
          <AccountHint view={view} />
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
