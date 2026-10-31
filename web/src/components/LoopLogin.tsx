import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import { api, type CreateLoopReq, type LoopView } from '../api'
import { LOGIN_SAY_SO, loginAccount, loginOptions, loopLogin } from '../claudeLogins'
import { EnvironmentPick } from './CloudLoopSetup'
import { useMay } from './Session'

// The Claude login a loop runs on (ADR-0060), in its Workstation card. An
// admin moves a contained or cloud loop to another; a bare loop names
// none, so its row only says which it runs on. A loop on a signed-out
// login waits for it, which the row says wherever it is read.
//
// A cloud loop's session runs under its login's account, in an
// environment of that account (#1078), so it moves only while paused, and
// a loop that has an environment names one on the new account in the same
// request: the hub refuses a move to an account that doesn't list it.
export function LoopLogin({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const manage = useMay()('manage')
  const [error, setError] = useState('')
  // The login a cloud loop is moving to while its new environment is picked.
  const [moving, setMoving] = useState('')
  const { data } = useQuery({ queryKey: ['claude-logins'], queryFn: api.claudeLogins })
  const current = loopLogin(loop)
  const login = data?.logins.find((l) => l.name === current)
  const move = useMutation({
    mutationFn: (change: Partial<CreateLoopReq>) => api.patchLoop(loop.name, change),
    onSuccess: () => {
      setError('')
      setMoving('')
      qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      qc.invalidateQueries({ queryKey: ['loops'] })
      qc.invalidateQueries({ queryKey: ['cloud-setup', loop.name] })
    },
    onError: (e: unknown) => setError(e instanceof Error ? e.message : String(e)),
  })
  const cloud = loop.runtime === 'cloud'
  const paused = loop.status === 'paused'
  const pick = (name: string) => {
    setError('')
    if (name === current) setMoving('')
    else if (cloud && loop.cloud_environment_id) setMoving(name)
    else move.mutate({ claude_login: name })
  }
  const signedOut = loop.runtime !== 'bare' && login?.signed_in === false

  return (
    <>
      {manage && loop.runtime !== 'bare' && data ? (
        <>
          <div className="row" style={{ marginTop: 8 }}>
            <label className="k" htmlFor="claude-login-select">
              claude login
            </label>
          </div>
          <select
            id="claude-login-select"
            className="panel-select"
            value={moving || current}
            disabled={move.isPending || (cloud && !paused)}
            onChange={(e) => pick(e.target.value)}
          >
            {loginOptions(data.logins).map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          {moving && (
            <div className="panel-note">
              Its environment, <code>{loop.cloud_environment_id}</code>, is on {loginAccount(current)}. Pick
              the one it moves to on {loginAccount(moving)}:
              <EnvironmentPick
                login={moving}
                chosen=""
                onPick={(id) => move.mutate({ claude_login: moving, cloud_environment_id: id })}
              />
              None yet? Make one on claude.ai/code signed in to {loginAccount(moving)}, named after the loop
              (the Environment step of its setup says how), then pick it here.
              <div style={{ marginTop: 8 }}>
                <button type="button" className="btn sm" onClick={() => setMoving('')}>
                  Cancel
                </button>
              </div>
            </div>
          )}
          <div className="panel-note">
            {!cloud
              ? 'A move takes effect from the next turn, and the session is kept.'
              : paused
                ? "A move starts a new session under the new login's account, in an environment of that account, and the loop needs a new hub token and a passed check before it runs again."
                : "Pause the loop to move it: its sessions run under its login's account."}{' '}
            {LOGIN_SAY_SO}
          </div>
        </>
      ) : (
        <div className="row">
          <span className="k">claude login</span>
          <span className="v">{current}</span>
        </div>
      )}
      {signedOut && (
        <div className="panel-note bad">
          The login {current} is signed out, so this loop waits until it is signed in again
          {loop.runtime !== 'bare' && ' or the loop moves to another'}. Sign in under{' '}
          <Link className="text-link" to="/settings">
            Settings → Claude logins
          </Link>
          .
        </div>
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </>
  )
}
