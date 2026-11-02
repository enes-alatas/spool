import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type PromptsChatDraft } from '../api'
import { egressAllows } from '../egress'
import { PromptsChatLogo } from './PromptsChatLogo'

// New loop's import from a prompts.chat bot (#1264, ADR-0063): a bot's URL
// in, the hub's draft out, which the form takes into its own fields for the
// operator to edit. Nothing is created here; Create does that, as without
// an import. Closed it is one button, so a form most loops fill by hand
// keeps Name first; open it takes the URL, and a refusal says the hub's
// reason under it. A draft taken closes it again.
export function PromptsChatFill({ onDraft }: { onDraft: (draft: PromptsChatDraft) => void }) {
  const [open, setOpen] = useState(false)
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const close = () => {
    setOpen(false)
    setUrl('')
    setError('')
  }

  const fill = async () => {
    if (!url.trim()) return
    setBusy(true)
    setError('')
    try {
      onDraft(await api.importPromptsChat(url.trim()))
      close()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (!open)
    return (
      <div className="field">
        <button type="button" className="btn import-open" onClick={() => setOpen(true)}>
          <PromptsChatLogo />
          Import a bot from prompts.chat
        </button>
      </div>
    )

  return (
    <div className="field import-field">
      {/* no label above it: the field opens where the button was, from the
          button's mark, which rolls to the field's right end and spins
          there while the hub fetches the bot */}
      <div className="import-row">
        <span className={`import-input${busy ? ' fetching' : ''}`}>
          <PromptsChatLogo />
          <input
            id="nl-import"
            aria-label="Bot on prompts.chat"
            // opened by the button, so the URL can be pasted at once
            autoFocus
            inputMode="url"
            placeholder="https://prompts.chat/bots/…"
            value={url}
            disabled={busy}
            aria-invalid={error !== ''}
            onChange={(e) => {
              setUrl(e.target.value)
              setError('')
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void fill()
              if (e.key === 'Escape') close()
            }}
          />
        </span>
        <button
          type="button"
          className="btn primary"
          onClick={() => void fill()}
          disabled={busy || !url.trim()}
        >
          {busy ? 'Importing…' : 'Import'}
        </button>
        <button type="button" className="btn" onClick={close} disabled={busy}>
          Cancel
        </button>
      </div>
      {error ? (
        <div className="form-error" role="alert">
          {error}
        </div>
      ) : (
        <div className="hint">Its page's address. The bot fills the fields below, for you to edit.</div>
      )}
    </div>
  )
}

// Where a filled form came from, under the mission: the mission is the
// bot's text, not the operator's, and reading it is the only check on it
// (ADR-0063), so the note says so and links the page to compare with.
export function ImportedFrom({ source }: { source: string }) {
  return (
    <div className="hint">
      Filled from{' '}
      <a href={source} target="_blank" rel="noopener noreferrer">
        {source.replace(/^https:\/\//, '')}
      </a>
      . This mission is the bot's text, not yours: read all of it before you create the loop.
    </div>
  )
}

// The hosts a bot's connectors are on, as suggestions (#1264). Egress is
// the fleet's allowlist, not the loop's, so a host is added on its own
// click, never by Create: an untouched draft widens nothing. A host the
// list already lets through says so instead.
export function EgressHints({ hints }: { hints: PromptsChatDraft['egress'] }) {
  const qc = useQueryClient()
  const { data: view, error: loadError } = useQuery({ queryKey: ['egress'], queryFn: api.egress })
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  if (hints.length === 0) return null

  const add = async (host: string) => {
    setBusy(host)
    setError('')
    try {
      qc.setQueryData(['egress'], await api.addEgressHost(host))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  return (
    <div className="field">
      <label>Egress the bot's connectors use</label>
      <ul className="egress-hints">
        {hints.map(({ host, connector }) => {
          const allowed = view ? egressAllows(view, host) : false
          return (
            <li key={host}>
              <code>{host}</code>
              <span className="egress-hint-connector">{connector}</span>
              {allowed ? (
                <span className="egress-hint-allowed">allowed</span>
              ) : (
                <button
                  type="button"
                  className="btn sm"
                  onClick={() => void add(host)}
                  disabled={!view || busy !== ''}
                >
                  {busy === host ? 'Adding…' : 'Add'}
                </button>
              )}
            </li>
          )
        })}
      </ul>
      {(error || loadError) && (
        <div className="form-error" role="alert">
          {error || (loadError instanceof Error ? loadError.message : String(loadError))}
        </div>
      )}
      <div className="hint">
        Add only what this loop needs. Each host goes on Settings → Egress, which every docker loop's
        workstation and every cloud loop's allowed domains share. A host that lets a loop post to people, like
        Slack's or Telegram's, lets it skip Spool's routing.
      </div>
    </div>
  )
}
