import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type UserLink, type WaitingLink } from '../api'
import { formatAgo } from '../format'
import { linkName, linkSnapshot, type OpenLinkCode } from '../links'
import { useMinuteClock } from './PlanUsage'
import { StepDialog } from './StepDialog'
import { CheckIcon } from './Icons'

// app is what a loop answers on there, as the page names it in a direction.
const SURFACES: { surface: UserLink['surface']; label: string; app: string }[] = [
  { surface: 'telegram', label: 'Telegram', app: 'Telegram bot' },
  { surface: 'slack', label: 'Slack', app: 'Slack app' },
]

// How often an open code asks whether it has been used. The bot answers the
// person in their DM at once; the page only has to notice soon after.
const POLL_MS = 3000

// Settings → Account's chat accounts (#704): who the signed-in user is on
// Telegram and Slack, which is how the room knows which private
// conversations are theirs to read (ADR-0050). A user links themselves by
// sending a code from their own account and saying YES when the bot asks
// (ADR-0051); nobody links anyone else.
export function LinksSection() {
  const qc = useQueryClient()
  const now = useMinuteClock()
  // The code the dialog walks through, from asked for to linked.
  const [code, setCode] = useState<OpenLinkCode | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // While the dialog is open, watch for the bot its code reaches and then
  // the link; while a code waits for YES, for the link or the question to
  // lapse, with the dialog closed too. One snapshot of both lists, so the
  // page never sees a question gone without the link it made.
  const { data, error: loadError } = useQuery({
    queryKey: ['me', 'links'],
    queryFn: () => linkSnapshot(api.myWaitingLinks, api.myLinks),
    refetchInterval: (query) => (code || query.state.data?.waiting.length ? POLL_MS : false),
  })
  const links = data?.links
  const waitingLinks = data?.waiting ?? []

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
  // A code is for one surface; asking again replaces it, as the hub keeps
  // one open code per user.
  const askCode = (surface: UserLink['surface']) =>
    run(async () => setCode({ ...(await api.linkCode(surface)), surface }))
  const remove = (surface: UserLink['surface']) =>
    run(async () => {
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
            {SURFACES.map(({ surface, label, app }) => {
              const link = links.find((l) => l.surface === surface)
              const wait = waitingLinks.find((l) => l.surface === surface)
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
                  ) : wait ? (
                    <Waiting link={wait} app={app} />
                  ) : (
                    <>
                      <span className="link-who link-none">not linked</span>
                      <button className="btn sm" onClick={() => askCode(surface)} disabled={busy}>
                        Link {label}
                      </button>
                    </>
                  )}
                </div>
              )
            })}
          </div>
        )
      )}

      {code && (
        <LinkDialog
          // A new code starts its steps over.
          key={code.code}
          code={code}
          surface={SURFACES.find((s) => s.surface === code.surface) ?? SURFACES[0]}
          link={links?.find((l) => l.surface === code.surface)}
          wait={waitingLinks.find((l) => l.surface === code.surface)}
          onAgain={() => askCode(code.surface)}
          onClose={() => setCode(null)}
        />
      )}

      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}

// A code that reached a loop's bot, which asked its sender there whether to
// link (ADR-0051). Only that YES links; the page waits for it.
function Waiting({ link, app }: { link: WaitingLink; app: string }) {
  const until = new Date(link.expires_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return (
    <span className="link-who">
      <span className="link-waiting">Waiting for YES.</span>{' '}
      <span className="link-since">
        @{link.loop}'s {app} got the code from {linkName(link)} and asked there whether to link it. Reply YES
        in that DM by {until}.
      </span>
    </span>
  )
}

// A code from asked for to linked (ADR-0051), in a dialog that stays open
// until the YES lands: copy it, send it to a bot, say YES when the bot asks.
// Each step ticks as the hub reports it. Closed early, the code still works,
// and the surface's row says when it is waiting for YES.
function LinkDialog({
  code,
  surface: { label, app },
  link,
  wait,
  onAgain,
  onClose,
}: {
  code: OpenLinkCode
  surface: (typeof SURFACES)[number]
  link?: UserLink
  wait?: WaitingLink
  onAgain: () => void
  onClose: () => void
}) {
  const [copied, setCopied] = useState<'' | 'ok' | 'failed'>('')
  // The question the code raised, kept once its waiting row goes: with the
  // link, the step still says who was asked; without one, the code was not
  // unsent, the question lapsed.
  const [asked, setAsked] = useState<WaitingLink | undefined>(wait)
  if (wait && wait !== asked) setAsked(wait)
  const expired = useExpired(asked ? asked.expires_at : code.expires_at)
  const at = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

  // The clipboard API exists only in a secure context, as on the Slack step.
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code.code)
      setCopied('ok')
    } catch {
      setCopied('failed')
    }
  }

  const failed = !link && (asked ? !wait : expired)
  const step = link ? 3 : asked ? 2 : copied === 'ok' ? 1 : 0
  const steps = [
    'Copy the code.',
    `Send it, on its own, to any loop's ${app} in a direct message, by ${at(code.expires_at)}.`,
    asked
      ? `Reply YES there: @${asked.loop}'s ${app} asked ${linkName(asked)} whether to link, until ${at(asked.expires_at)}.`
      : 'Reply YES when it asks whether to link the account you sent it from.',
  ]

  return (
    <StepDialog title={`Link ${label}`} className="link-dialog" onClose={onClose}>
      <div className="otp">
        <code className="otp-value">{code.code}</code>
        <button className="btn sm" onClick={copy} disabled={!!link}>
          {copied === 'ok' ? 'Copied' : 'Copy'}
        </button>
      </div>
      {copied === 'failed' && (
        <div className="form-error" role="alert">
          The browser refused the clipboard; select the code instead.
        </div>
      )}
      <ol className="link-steps">
        {steps.map((text, i) => (
          <li key={i} className={i < step ? 'done' : i === step && !failed ? 'now' : ''}>
            <span className="link-step-mark" aria-hidden>
              {i < step ? <CheckIcon size={12} /> : i + 1}
            </span>
            {text}
          </li>
        ))}
      </ol>
      {link ? (
        <p className="step-dialog-state">
          <span className="ok">
            <CheckIcon size={14} /> Linked: {linkName(link)}.
          </span>
        </p>
      ) : failed ? (
        <p className="step-dialog-state">
          {asked
            ? 'No YES came in time, so nothing was linked.'
            : 'The code expired before it reached a bot.'}
        </p>
      ) : null}
      {link ? (
        <form method="dialog">
          <button className="btn primary">Done</button>
        </form>
      ) : (
        failed && (
          <button className="btn" onClick={onAgain}>
            Get a new code
          </button>
        )
      )}
    </StepDialog>
  )
}

// Whether the hub's deadline has passed, by this browser's clock, turning
// true when it does.
function useExpired(at: number): boolean {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = setTimeout(() => setNow(Date.now()), Math.max(0, at - Date.now()))
    return () => clearTimeout(timer)
  }, [at])
  return now >= at
}
