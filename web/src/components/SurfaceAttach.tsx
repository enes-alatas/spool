import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api, type LoopView } from '../api'
import { tokenSubmittable } from '../forms'
import { slackCreateAppURL, slackManifest } from '../slackManifest'
import { hubHasSlack, slackAttachError, type SlackAttachError, slackPairSubmittable } from '../slack'

// Binding a loop to a Telegram bot, and rebinding it to another. The token is
// write-only in the same sense as a secret: it is typed, sent, and never
// rendered back — the panel above says which bot answers, which is the part
// an operator needs to recognise.
//
// It lives on the loop page because that is where a rotation is noticed, and
// the first-run Chat surface card opens it in a dialog (#589). The
// old copy sent the operator to "loop settings" for a control that was never
// built, so the only way to change a token was the API; the 2026-09-18
// rotation was three curl calls (#157).
//
// Opened by a caller that owns the choice of surface (`startOpen`), it is
// that step alone and hands the choice back on close (`onClose`).
export function BotTokenForm({
  loop,
  startOpen,
  onClose,
}: {
  loop: LoopView
  startOpen?: boolean
  onClose?: () => void
}) {
  const qc = useQueryClient()
  const [open, setOpen] = useState(startOpen ?? false)
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const close = () => {
    setOpen(false)
    setToken('')
    setError('')
    onClose?.()
  }

  const save = async () => {
    setBusy(true)
    setError('')
    try {
      await api.patchLoop(loop.name, { tg_bot_token: token.trim() })
      // The loop, not just this panel: the bound username is the server's
      // answer to whether the token worked, and the header's surface line
      // reads from the same record.
      qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      qc.invalidateQueries({ queryKey: ['loops'] })
      close()
    } catch (e) {
      // The server's own words. It validates against Telegram before storing,
      // so "telegram token rejected: …" distinguishes a typo from a revoked
      // token — and it cannot quote the token back, which is redacted at the
      // client (#146).
      setError(e instanceof Error ? e.message : 'could not save the token')
    } finally {
      setBusy(false)
    }
  }

  if (!open) {
    return (
      <button className="btn sm" style={{ marginTop: 10 }} onClick={() => setOpen(true)}>
        {loop.has_tg_token ? 'Replace token' : 'Attach Telegram'}
      </button>
    )
  }
  return (
    <div className="field" style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 6 }}>
      <input
        type="password"
        placeholder="bot token from @BotFather"
        value={token}
        onChange={(e) => setToken(e.target.value)}
        // so a browser does not offer to remember a bot token. Not a
        // guarantee — browsers honour this unevenly on password fields — but
        // this input is in no form, which is the other half of not being
        // treated as a login.
        autoComplete="off"
        autoFocus
      />
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div style={{ display: 'flex', gap: 6 }}>
        {/* `tokenSubmittable` is load-bearing, not tidiness — see its comment:
            PATCH reads an empty `tg_bot_token` as *disconnect*. */}
        <button className="btn primary" onClick={save} disabled={busy || !tokenSubmittable(token)}>
          {busy ? 'Checking…' : 'Save'}
        </button>
        <button className="btn" onClick={close} disabled={busy}>
          Cancel
        </button>
      </div>
    </div>
  )
}

// Attaching Slack: the app the loop will run as (#345), then the two tokens
// it yields (#230). The manifest carries the scopes, so the fields only need
// to say where each token is found. On a hub without the surface the fields
// would store nothing, so the step says so instead of offering them.
export function SlackStep({ loop, onClose }: { loop: LoopView; onClose: () => void }) {
  const manifest = useMemo(() => slackManifest(loop.name), [loop.name])
  const text = useMemo(() => JSON.stringify(manifest, null, 2), [manifest])
  const [copied, setCopied] = useState<'' | 'ok' | 'failed'>('')

  // The clipboard API exists only in a secure context: a hub reached over
  // plain http on a LAN address has none, so the failure says what to do
  // instead of leaving a button that did nothing.
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied('ok')
    } catch {
      setCopied('failed')
    }
  }

  return (
    <div className="slack-step">
      <ol>
        <li>
          Create the loop's Slack app from this manifest, in the workspace the loop will work in. The link
          opens Slack with it filled in.
        </li>
        <li>
          Install it to the workspace. The bot token, <code>xoxb-…</code>, is under OAuth &amp; Permissions.
        </li>
        <li>
          Under Basic Information, generate an app-level token, <code>xapp-…</code>, with the{' '}
          <code>connections:write</code> scope. A manifest cannot create this one.
        </li>
      </ol>
      <pre className="slack-manifest">{text}</pre>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        <a className="btn sm primary" href={slackCreateAppURL(manifest)} target="_blank" rel="noreferrer">
          Create app from manifest
        </a>
        <button className="btn sm" onClick={copy}>
          {copied === 'ok' ? 'Copied' : 'Copy manifest'}
        </button>
        <button className="btn sm" onClick={onClose}>
          Close
        </button>
      </div>
      {copied === 'failed' && (
        <div className="form-error" role="alert">
          The browser refused the clipboard; select the text instead.
        </div>
      )}
      {hubHasSlack(loop) ? (
        <SlackTokenForm loop={loop} onDone={onClose} />
      ) : (
        <div className="hint">Token paste arrives with the Slack surface (#230).</div>
      )}
    </div>
  )
}

// The token pair, for attaching or rotating. Both go in one PATCH: a Slack
// app is one pair, and the hub checks both with Slack before it stores
// either, so a rejected pair never reaches the loop. The error names the
// field Slack refused when it was one of the two.
export function SlackTokenForm({ loop, onDone }: { loop: LoopView; onDone: () => void }) {
  const qc = useQueryClient()
  const [appToken, setAppToken] = useState('')
  const [botToken, setBotToken] = useState('')
  const [error, setError] = useState<SlackAttachError | null>(null)
  const [busy, setBusy] = useState(false)
  const rotating = !!loop.has_slack_tokens

  const save = async () => {
    setBusy(true)
    setError(null)
    try {
      await api.patchLoop(loop.name, { slack_app_token: appToken.trim(), slack_bot_token: botToken.trim() })
      qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      qc.invalidateQueries({ queryKey: ['loops'] })
      qc.invalidateQueries({ queryKey: ['slack-status', loop.name] })
      onDone()
    } catch (e) {
      setError(slackAttachError(e))
    } finally {
      setBusy(false)
    }
  }

  const errorID = `slack-token-error-${loop.name}`
  return (
    <div className="slack-tokens">
      <div className="field">
        <label htmlFor={`slack-app-token-${loop.name}`}>app-level token</label>
        <input
          id={`slack-app-token-${loop.name}`}
          type="password"
          placeholder="xapp-…"
          value={appToken}
          onChange={(e) => setAppToken(e.target.value)}
          // as for the Telegram token: not a login to remember
          autoComplete="off"
          aria-invalid={error?.field === 'app' || undefined}
          aria-describedby={error?.field === 'app' ? errorID : undefined}
        />
        <div className="hint">Basic Information, App-Level Tokens.</div>
      </div>
      <div className="field">
        <label htmlFor={`slack-bot-token-${loop.name}`}>bot token</label>
        <input
          id={`slack-bot-token-${loop.name}`}
          type="password"
          placeholder="xoxb-…"
          value={botToken}
          onChange={(e) => setBotToken(e.target.value)}
          autoComplete="off"
          aria-invalid={error?.field === 'bot' || undefined}
          aria-describedby={error?.field === 'bot' ? errorID : undefined}
        />
        <div className="hint">OAuth &amp; Permissions, after installing the app.</div>
      </div>
      {rotating && (
        <div className="hint">Saving replaces both tokens. The loop keeps its channel and its owner.</div>
      )}
      {error && (
        <div className="form-error" role="alert" id={errorID}>
          {error.message}
        </div>
      )}
      <div style={{ display: 'flex', gap: 6 }}>
        {/* Both or neither: an empty pair is how PATCH detaches. */}
        <button
          className="btn primary"
          onClick={save}
          disabled={busy || !slackPairSubmittable(appToken, botToken)}
        >
          {busy ? 'Checking with Slack…' : rotating ? 'Replace tokens' : 'Attach'}
        </button>
        <button className="btn" onClick={onDone} disabled={busy}>
          Cancel
        </button>
      </div>
    </div>
  )
}
