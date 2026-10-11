import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type LinkCode, type UserLink } from '../api'
import { formatAgo } from '../format'
import { linkName, spent } from '../links'
import { useMinuteClock } from './PlanUsage'

const SURFACES: { surface: UserLink['surface']; label: string }[] = [
  { surface: 'telegram', label: 'Telegram' },
  { surface: 'slack', label: 'Slack' },
]

// How often an open code asks whether it has been used. The bot answers the
// person in their DM at once; the page only has to notice soon after.
const POLL_MS = 3000

// Settings → Account's chat accounts (#704): who the signed-in user is on
// Telegram and Slack, which is how the room knows which private
// conversations are theirs to read (ADR-0050). A user links themselves by
// sending a code from their own account; nobody links anyone else.
export function LinksSection() {
  const qc = useQueryClient()
  const now = useMinuteClock()
  const [code, setCode] = useState<LinkCode | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { data: links, error: loadError } = useQuery({
    queryKey: ['me', 'links'],
    queryFn: api.myLinks,
    // While a code is open, watch for the link it makes.
    refetchInterval: (query) => (code && !spent(code, query.state.data) ? POLL_MS : false),
  })

  // A spent code goes, and so does an expired one, which links nothing.
  const open = code && !spent(code, links) ? code : null
  useEffect(() => {
    if (!code) return
    const timer = setTimeout(() => setCode(null), Math.max(0, code.expires_at - Date.now()))
    return () => clearTimeout(timer)
  }, [code])

  const run = async (request: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await request()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  const askCode = () => run(async () => setCode(await api.linkCode()))
  const remove = (surface: UserLink['surface']) =>
    run(async () => {
      // A spent code would read as open again once its link is gone.
      setCode(null)
      await api.removeLink(surface)
      await qc.invalidateQueries({ queryKey: ['me', 'links'] })
    })

  return (
    <div className="links">
      <p className="page-lede">
        Who you are on Telegram and Slack. Linked, the room shows you your private conversations with the
        loops.
      </p>
      {loadError ? (
        <div className="form-error" role="alert">
          Could not load your chat accounts: {loadError.message}
        </div>
      ) : (
        links && (
          <div className="link-list">
            {SURFACES.map(({ surface, label }) => {
              const link = links.find((l) => l.surface === surface)
              return (
                <div className="link-row" key={surface}>
                  <span className="link-surface">{label}</span>
                  {link ? (
                    <>
                      <span className="link-who">
                        {linkName(link)}
                        <span className="link-since"> · linked {formatAgo(link.created_at, now)}</span>
                      </span>
                      <button className="btn sm" onClick={() => remove(surface)} disabled={busy}>
                        Remove
                      </button>
                    </>
                  ) : (
                    <span className="link-who link-none">not linked</span>
                  )}
                </div>
              )
            })}
          </div>
        )
      )}

      {open ? (
        <OpenCode code={open} />
      ) : (
        <button className="btn" onClick={askCode} disabled={busy || !links}>
          Get a link code
        </button>
      )}

      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}

function OpenCode({ code }: { code: LinkCode }) {
  const [copied, setCopied] = useState<'' | 'ok' | 'failed'>('')
  const until = new Date(code.expires_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

  // The clipboard API exists only in a secure context, as on the Slack step.
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code.code)
      setCopied('ok')
    } catch {
      setCopied('failed')
    }
  }

  return (
    <div className="link-code">
      <div className="otp">
        <code className="otp-value">{code.code}</code>
        <button className="btn sm" onClick={copy}>
          {copied === 'ok' ? 'Copied' : 'Copy'}
        </button>
      </div>
      {copied === 'failed' && (
        <div className="form-error" role="alert">
          The browser refused the clipboard; select the code instead.
        </div>
      )}
      <p className="link-code-note">
        Send it, on its own, to any loop's Telegram bot or Slack app in a direct message. It links the account
        you send it from, once, until {until}. This page shows the link when it lands.
      </p>
    </div>
  )
}
