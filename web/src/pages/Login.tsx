import { useState, type FormEvent } from 'react'
import { api } from '../api'
import { tokenSubmittable } from '../forms'
import { loginError } from '../session'
import { SpoolGlyph } from '../components/Spool'

// The one screen shown without a session (#239). It asks for the operator
// token once, trades it for the cookie, and is never seen again until that
// cookie is gone — the hub's own credential is not something to be asked for
// on every visit.
export default function Login({ onSignedIn }: { onSignedIn: () => void }) {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    // A form rather than a button, so the paste-and-Enter that every
    // credential field is used with works without a key handler.
    e.preventDefault()
    if (!tokenSubmittable(token) || busy) return
    setBusy(true)
    setError('')
    try {
      await api.login(token.trim())
      // The token stops existing here: what authenticates from now on is the
      // cookie the hub just set, which this cannot read and does not need to.
      setToken('')
      onSignedIn()
    } catch (e) {
      setError(loginError(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <div className="wordmark">
          <SpoolGlyph size={26} />
          spool
        </div>

        <p className="page-lede">
          This hub answers to its operator token. Paste it once — the session lives in a cookie this page
          can't read, and it survives a reload.
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
            Run <code>spool token</code> where the hub runs to print it. It was also printed once, on the
            hub's first start.
          </div>
        </div>

        {error && <div className="form-error">{error}</div>}

        <button className="btn primary" type="submit" disabled={busy || !tokenSubmittable(token)}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
