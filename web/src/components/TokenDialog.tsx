import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import { tokenSubmittable } from '../forms'
import { CheckIcon } from './Icons'

// The Harness card's setup-token dialog (#588): the token field Settings has,
// over the first-run page, so setting up the harness doesn't leave it.
// Saving a token starts the hub's login check (ADR-0044); the dialog stays
// open to show what the check found, and closing it lands back on the page.
//
// A native modal `<dialog>`, as Add connection is: backdrop, focus trap and
// Escape come with it.
export function TokenDialog({ replacing, onClose }: { replacing: boolean; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  const qc = useQueryClient()
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    const dialog = ref.current
    if (dialog && !dialog.open) dialog.showModal()
  }, [])

  // Read only once a token is saved, and quickly while its check runs.
  const { data: onboarding } = useQuery({
    queryKey: ['onboarding'],
    queryFn: api.onboarding,
    enabled: saved,
    refetchInterval: (q) => (q.state.data?.harness.checking ? 2000 : false),
    retry: false,
  })

  const save = async () => {
    setBusy(true)
    setError('')
    const message = (e: unknown) => (e instanceof Error ? e.message : String(e))
    try {
      await api.setClaudeToken(token.trim())
    } catch (e) {
      setError(message(e))
      setBusy(false)
      return
    }
    setToken('')
    qc.invalidateQueries({ queryKey: ['settings'] })
    try {
      // The hub records the check as pending before the save answers, so
      // one read after it says checking. Until that read lands, the cache
      // still holds the last token's outcome, which is not this one's.
      qc.setQueryData(['onboarding'], await api.onboarding())
      setSaved(true)
    } catch (e) {
      setError(`Token saved, but its check could not be read: ${message(e)}`)
    } finally {
      setBusy(false)
    }
  }

  const harness = saved ? onboarding?.harness : undefined
  const close = () => ref.current?.close()

  return (
    <dialog
      ref={ref}
      className="connection-dialog token-dialog"
      aria-labelledby="token-dialog-title"
      onClose={onClose}
      // A click on the backdrop lands on the dialog element itself.
      onClick={(e) => e.target === e.currentTarget && close()}
    >
      <div className="panel-head">
        <h3 id="token-dialog-title">Claude setup-token</h3>
        <button className="panel-action" onClick={close} aria-label="Close" title="Close">
          ✕
        </button>
      </div>
      {saved ? (
        <div className="form">
          <p className="token-dialog-state" aria-live="polite">
            {harness?.done ? (
              <span className="ok">
                <CheckIcon size={14} /> Token saved. Loops have a Claude login to use.
              </span>
            ) : harness && !harness.checking ? (
              <>
                Token saved, but the login check didn't pass.
                <span className="token-dialog-reason">{harness.reason}</span>
              </>
            ) : (
              <span>Token saved. Checking the login…</span>
            )}
          </p>
          <div className="pillar-actions">
            <button className="btn primary" onClick={close}>
              {harness?.done ? 'Done' : 'Close'}
            </button>
            {harness && !harness.done && !harness.checking && (
              <button className="btn" onClick={() => setSaved(false)}>
                Try another token
              </button>
            )}
          </div>
        </div>
      ) : (
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault()
            if (tokenSubmittable(token)) void save()
          }}
        >
          <div className="field">
            <label htmlFor="token-dialog-input">Token</label>
            <input
              id="token-dialog-input"
              type="password"
              autoComplete="off"
              autoFocus
              placeholder={replacing ? 'Paste a new token to replace it' : 'sk-ant-oat01-…'}
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
            <div className="hint">
              Run <code>claude setup-token</code> where you're logged in, then paste the result here. It's
              stored write-only, and saving it checks the login once with one small call on your plan.
            </div>
          </div>
          {error && (
            <div className="form-error" role="alert">
              {error}
            </div>
          )}
          <div className="pillar-actions">
            <button type="submit" className="btn primary" disabled={busy || !tokenSubmittable(token)}>
              {busy ? 'Saving…' : 'Save and check'}
            </button>
          </div>
        </form>
      )}
    </dialog>
  )
}
