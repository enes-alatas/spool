import { useState, type FormEvent } from 'react'
import { api } from '../api'
import { newPasswordProblem, passwordError, PASSWORD_MAX, PASSWORD_MIN } from '../session'
import { SpoolGlyph } from '../components/Spool'
import { useSignOut } from '../components/Session'

// A user signed in with a one-time password chooses their own before
// anything else (#582). The hub refuses every other route until they do,
// so this is the whole room until then; the one way out is Sign out.
export default function ChangePassword({ name, onChanged }: { name: string; onChanged: () => void }) {
  const { signOut, busy: signingOut } = useSignOut()
  const [password, setPassword] = useState('')
  const [again, setAgain] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const problem = newPasswordProblem(password, again)
  const submittable = password !== '' && again === password && problem === ''

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!submittable || busy) return
    setBusy(true)
    setError('')
    try {
      // The session has just proved the one-time password, so the hub does
      // not ask for it again.
      await api.changePassword({ new_password: password })
      setPassword('')
      setAgain('')
      onChanged()
    } catch (e) {
      setError(passwordError(e))
      setBusy(false)
    }
  }

  return (
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <div className="wordmark">
          <SpoolGlyph size={26} />
          <span className="wordmark-text">spool</span>
        </div>

        <h1 className="login-title">Choose your password</h1>
        <p className="page-lede">
          You signed in as <b>{name}</b> with a one-time password. Choose your own before you go on.
        </p>

        <div className="field">
          <label htmlFor="new-password">New password</label>
          <input
            id="new-password"
            type="password"
            autoComplete="new-password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <div className="hint">
            {PASSWORD_MIN} to {PASSWORD_MAX} characters. Any characters; a long phrase is fine.
          </div>
        </div>
        <div className="field">
          <label htmlFor="new-password-again">New password, again</label>
          <input
            id="new-password-again"
            type="password"
            autoComplete="new-password"
            value={again}
            onChange={(e) => setAgain(e.target.value)}
          />
          {/* Said once both are typed, not while the first one is: a
              length warning on the first keystroke scolds a password that
              is not finished yet. */}
          {problem && again !== '' && <div className="hint warn">{problem}</div>}
        </div>

        {error && (
          <div className="form-error" role="alert">
            {error}
          </div>
        )}

        <button className="btn primary" type="submit" disabled={busy || !submittable}>
          {busy ? 'Saving…' : 'Set password'}
        </button>
        <button className="login-alt" type="button" onClick={signOut} disabled={busy || signingOut}>
          Sign out
        </button>
      </form>
    </div>
  )
}
