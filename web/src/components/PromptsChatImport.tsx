import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type PromptsChatDraft, type PromptsChatSkill, type PromptsChatTeam } from '../api'
import { installable, skillSize } from '../skills'
import { isTeamAddress } from '../team'
import { egressAllows } from '../egress'
import { PromptsChatLogo } from './PromptsChatLogo'

// New loop's import from a prompts.chat bot (#1264, ADR-0063): a bot's URL
// in, the hub's draft out, which the form takes into its own fields for the
// operator to edit. Nothing is created here; Create does that, as without
// an import. Closed it is one button, so a form most loops fill by hand
// keeps Name first; open it takes the URL, and a refusal says the hub's
// reason under it. A draft taken closes it again. Given onTeam, it takes a
// team's page as well (#1266) and hands over the team's draft instead.
export function PromptsChatFill({
  onDraft,
  onTeam,
}: {
  onDraft: (draft: PromptsChatDraft) => void
  onTeam?: (team: PromptsChatTeam) => void
}) {
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
      const address = url.trim()
      if (onTeam && isTeamAddress(address)) onTeam(await api.importPromptsChatTeam(address))
      else onDraft(await api.importPromptsChat(address))
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
          {onTeam ? 'Import a bot or a team from prompts.chat' : 'Import a bot from prompts.chat'}
        </button>
        <BrowseLinks teams={!!onTeam} />
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
            aria-label={onTeam ? 'Bot or team on prompts.chat' : 'Bot on prompts.chat'}
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
        <div className="hint">
          {onTeam
            ? "Its page's address. A bot fills the fields below, for you to edit; a team shows the loops and the channel it makes, before any exists."
            : "Its page's address. The bot fills the fields below, for you to edit."}
        </div>
      )}
      <BrowseLinks teams={!!onTeam} />
    </div>
  )
}

// Where to find a bot or a team to import (#1325): prompts.chat's own
// lists, opened in a new tab of the operator's browser. The hub fetches
// nothing for them. Teams are offered only where a team can be imported.
function BrowseLinks({ teams }: { teams: boolean }) {
  return (
    <div className="hint import-browse">
      <a className="text-link" href={PROMPTS_CHAT_BOTS} target="_blank" rel="noopener noreferrer">
        Browse bots ↗
      </a>
      {teams && (
        <a className="text-link" href={PROMPTS_CHAT_TEAMS} target="_blank" rel="noopener noreferrer">
          Browse teams ↗
        </a>
      )}
    </div>
  )
}

const PROMPTS_CHAT_BOTS = 'https://prompts.chat/bots'
const PROMPTS_CHAT_TEAMS = 'https://prompts.chat/bots/teams'

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

// The skills a bot was built to use (#1365, #1395). One the draft carries
// files for is ticked to install, and its files open to be read first: as
// plain text, exactly the bytes create writes, never rendered, since they
// are someone else's writing. Unticking leaves the whole skill out, never a
// file, as a SKILL.md can refer to the files beside it. One that isn't
// installed says why, in the hub's words. refused says why this loop
// can't take skills at all; then none is ticked.
export function SkillHints({
  skills,
  leftOut,
  onToggle,
  refused = '',
}: {
  skills: PromptsChatSkill[]
  leftOut: ReadonlySet<string>
  onToggle: (dir: string) => void
  refused?: string
}) {
  if (skills.length === 0) return null
  return (
    <>
      <ul className="skill-hints" aria-label="Skills the bot was built to use">
        {/* keyed by place too: the hub keeps a name a bot repeats */}
        {skills.map((skill, i) => {
          const files = installable(skill) && !refused
          const kept = files && !leftOut.has(skill.dir ?? '')
          return (
            <li key={`${i}:${skill.name}`}>
              {files && (
                <input
                  type="checkbox"
                  className="skill-hint-tick"
                  aria-label={`Install ${skill.name}`}
                  checked={kept}
                  onChange={() => onToggle(skill.dir ?? '')}
                />
              )}
              <span className="skill-hint-text">
                <span className="skill-hint-name">{skill.name}</span>
                {skill.description && <span className="skill-hint-description">{skill.description}</span>}
                {!skill.installed && skill.reason && (
                  <span className="skill-hint-reason">{skill.reason}</span>
                )}
                {files && (
                  <details className="skill-files">
                    <summary>{skillSize(skill)}</summary>
                    {(skill.files ?? []).map((f) => (
                      <details key={f.path} className="skill-file">
                        <summary>{f.path}</summary>
                        <pre>{f.content}</pre>
                      </details>
                    ))}
                  </details>
                )}
              </span>
              {kept ? (
                <span className="skill-hint-state installed">installs</span>
              ) : files ? (
                <span className="skill-hint-state">left out</span>
              ) : (
                <span className="skill-hint-state">not installed</span>
              )}
            </li>
          )
        })}
      </ul>
      {skills.some(installable) && (
        <div className="hint">
          {refused ||
            "Create writes each ticked skill's files into the loop's .claude/skills/. They are the bot author's text: read them first."}
        </div>
      )}
    </>
  )
}
