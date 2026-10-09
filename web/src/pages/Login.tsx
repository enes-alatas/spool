import { useState, type FormEvent } from 'react'
import { api } from '../api'
import { tokenSubmittable } from '../forms'
import { loginError } from '../session'
import { SpoolGlyph } from '../components/Spool'

// The one screen shown without a session (#239). A user signs in with a
// username and password (#582). The operator token signs in too for one
// release, behind a link, so a hub upgraded before its operator has read
// the new owner's one-time password is not locked out of its own room.
export default function Login({ onSignedIn }: { onSignedIn: () => void }) {
  const [mode, setMode] = useState<'password' | 'token'>('password')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submittable =
    mode === 'password' ? username.trim() !== '' && password !== '' : tokenSubmittable(token)

  const submit = async (e: FormEvent) => {
    // A form rather than a button, so the paste-and-Enter that every
    // credential field is used with works without a key handler.
    e.preventDefault()
    if (!submittable || busy) return
    setBusy(true)
    setError('')
    try {
      // A password is sent as typed: leading or trailing spaces may be part
      // of it. A username or a pasted token never has any.
      await api.login(mode === 'password' ? { username: username.trim(), password } : { token: token.trim() })
      // The credential stops existing here: what authenticates from now on
      // is the cookie the hub just set, which this cannot read and does not
      // need to.
      setPassword('')
      setToken('')
      onSignedIn()
    } catch (e) {
      setError(loginError(e))
    } finally {
      setBusy(false)
    }
  }

  const switchTo = (next: 'password' | 'token') => {
    setMode(next)
    setError('')
  }

  return (
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <div className="wordmark">
          <SpoolGlyph size={26} />
          <span className="wordmark-text">spool</span>
        </div>

        {mode === 'password' ? (
          <>
            <p className="page-lede">Sign in to this hub.</p>
            <div className="field">
              <label htmlFor="username">Username</label>
              <input
                id="username"
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                autoFocus
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor="password">Password</label>
              <input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <div className="hint">
                The hub's first start printed <code>admin</code>'s one-time password. Run{' '}
                <code>spool user reset admin</code> where the hub runs to print a new one.
              </div>
            </div>
          </>
        ) : (
          <>
            <p className="page-lede">
              Sign in with the operator token. This form goes away in the next release; sign in as a user
              instead.
            </p>
            <div className="field">
              <label htmlFor="operator-token">Operator token</label>
              <input
                id="operator-token"
                type="password"
                autoComplete="off"
                autoFocus
                placeholder="Paste the token"
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
              <div className="hint">
                Run <code>spool token</code> where the hub runs to print it.
              </div>
            </div>
          </>
        )}

        {error && (
          <div className="form-error" role="alert">
            {error}
          </div>
        )}

        <button className="btn primary" type="submit" disabled={busy || !submittable}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>

        {mode === 'password' && <div className="login-or">or</div>}
        <button
          className="login-alt"
          type="button"
          onClick={() => switchTo(mode === 'password' ? 'token' : 'password')}
        >
          {mode === 'password' ? 'Sign in with the operator token' : 'Sign in with a username and password'}
        </button>
      </form>
    </div>
  )
}
