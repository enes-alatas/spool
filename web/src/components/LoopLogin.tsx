import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import { api, type LoopView } from '../api'
import { LOGIN_SAY_SO, loginOptions, loopLogin } from '../claudeLogins'
import { useMay } from './Session'

// The Claude login a loop runs on (ADR-0060), in its Workstation card. An
// admin moves a contained loop to another; a bare or cloud loop names
// none, so its row only says which it runs on. A loop on a signed-out
// login waits for it, which the row says wherever it is read.
export function LoopLogin({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const manage = useMay()('manage')
  const [error, setError] = useState('')
  const { data } = useQuery({ queryKey: ['claude-logins'], queryFn: api.claudeLogins })
  const current = loopLogin(loop)
  const login = data?.logins.find((l) => l.name === current)
  const move = useMutation({
    mutationFn: (name: string) => api.patchLoop(loop.name, { claude_login: name }),
    onSuccess: () => {
      setError('')
      qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      qc.invalidateQueries({ queryKey: ['loops'] })
    },
    onError: (e: unknown) => setError(e instanceof Error ? e.message : String(e)),
  })
  const signedOut = loop.runtime !== 'bare' && login?.signed_in === false

  return (
    <>
      {manage && loop.runtime === 'docker' && data ? (
        <>
          <div className="row" style={{ marginTop: 8 }}>
            <label className="k" htmlFor="claude-login-select">
              claude login
            </label>
          </div>
          <select
            id="claude-login-select"
            className="panel-select"
            value={current}
            disabled={move.isPending}
            onChange={(e) => move.mutate(e.target.value)}
          >
            {loginOptions(data.logins).map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <div className="panel-note">
            A move takes effect from the next turn, and the session is kept. {LOGIN_SAY_SO}
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
          {loop.runtime === 'docker' && ' or the loop moves to another'}. Sign in under{' '}
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
