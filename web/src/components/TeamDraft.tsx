import { useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { api, type PromptsChatTeam, type PromptsChatTeamLoop } from '../api'
import { channelHash } from '../channels'
import { handoffLine } from '../team'

// A team read from prompts.chat (#1266, ADR-0063), shown on New loop in
// place of the form: what Create makes, read before anything exists.
// Create makes every loop and the channel holding them, or none of them,
// and lands on the channel. The names are the draft's and are not edited:
// each mission names its teammates by them, so a rename would leave the
// other missions pointing at a loop that isn't there. A loop is renamed on
// its own page after.
export function TeamDraft({ team, onCancel }: { team: PromptsChatTeam; onCancel: () => void }) {
  const nav = useNavigate()
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      const made = await api.createTeam({
        channel: team.channel,
        description: team.description,
        runtime: '',
        loops: team.loops.map(({ name, mission }) => ({ name, mission })),
      })
      await Promise.all([
        qc.invalidateQueries({ queryKey: ['loops'] }),
        qc.invalidateQueries({ queryKey: ['channels'] }),
      ])
      nav(`/channels${channelHash(made.channel)}`)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  const count = `${team.loops.length} loop${team.loops.length === 1 ? '' : 's'}`
  return (
    <TeamPreview team={team} count={count}>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="team-actions">
        <button type="button" className="btn primary" onClick={() => void create()} disabled={busy}>
          {busy ? 'Creating…' : `Create ${count} and #${team.channel}`}
        </button>
        <button type="button" className="btn" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </TeamPreview>
  )
}

// What Create makes, read before it does: the channel, then each loop with
// its role and handoffs, its mission folded away but one click from being
// read in full, since it is the bot's text, not the operator's.
function TeamPreview({
  team,
  count,
  children,
}: {
  team: PromptsChatTeam
  count: string
  children: ReactNode
}) {
  return (
    <section className="team-preview" aria-label="What Create makes">
      <div className="team-channel">
        <span className="channel-name">#{team.channel}</span>
        <span className="channel-count">{count}</span>
        {team.description && <div className="team-description">{team.description}</div>}
      </div>
      <ul className="team-loops">
        {team.loops.map((loop) => (
          <TeamMember key={loop.name} loop={loop} />
        ))}
      </ul>
      <div className="hint">
        Read from{' '}
        <a href={team.source} target="_blank" rel="noopener noreferrer">
          {team.source.replace(/^https:\/\//, '')}
        </a>
        . Each mission is a bot's text, not yours: read them before you create the team. Every loop runs on
        the hub's default runtime.
      </div>
      {children}
    </section>
  )
}

function TeamMember({ loop }: { loop: PromptsChatTeamLoop }) {
  return (
    <li className="team-loop">
      <div className="team-loop-head">
        <span className="team-loop-name">@{loop.name}</span>
        {loop.role && <span className="team-loop-role">{loop.role}</span>}
      </div>
      <div className="team-loop-handoffs">{handoffLine(loop)}</div>
      <details className="team-loop-mission">
        <summary>Mission</summary>
        <pre>{loop.mission}</pre>
      </details>
    </li>
  )
}
