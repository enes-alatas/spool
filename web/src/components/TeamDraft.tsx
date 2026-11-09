import { useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { api, type PromptsChatTeam, type PromptsChatTeamLoop } from '../api'
import { channelHash } from '../channels'
import { handoffLine } from '../team'
import { SkillHints } from './PromptsChatImport'
import { skillsToInstall } from '../skills'

const NONE: ReadonlySet<string> = new Set()

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
  // Each loop's unticked skills, by dir (#1395): every drafted loop carries
  // the same skills, and each is kept or left out on its own.
  const [leftOut, setLeftOut] = useState<ReadonlyMap<string, ReadonlySet<string>>>(new Map())
  const toggle = (loop: string, dir: string) =>
    setLeftOut((prev) => {
      const next = new Map(prev)
      const dirs = new Set(prev.get(loop))
      if (dirs.has(dir)) dirs.delete(dir)
      else dirs.add(dir)
      next.set(loop, dirs)
      return next
    })

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      const made = await api.createPromptsChatTeam({
        channel: team.channel,
        description: team.description,
        runtime: '',
        loops: team.loops.map(({ name, mission, skills }) => ({
          name,
          mission,
          skills: skillsToInstall(skills, leftOut.get(name) ?? NONE),
        })),
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
    <TeamPreview team={team} count={count} leftOut={leftOut} onToggle={toggle}>
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
  leftOut,
  onToggle,
  children,
}: {
  team: PromptsChatTeam
  count: string
  leftOut: ReadonlyMap<string, ReadonlySet<string>>
  onToggle: (loop: string, dir: string) => void
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
          <TeamMember
            key={loop.name}
            loop={loop}
            leftOut={leftOut.get(loop.name) ?? NONE}
            onToggle={(dir) => onToggle(loop.name, dir)}
          />
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

function TeamMember({
  loop,
  leftOut,
  onToggle,
}: {
  loop: PromptsChatTeamLoop
  leftOut: ReadonlySet<string>
  onToggle: (dir: string) => void
}) {
  return (
    <li className="team-loop">
      <div className="team-loop-head">
        <span className="team-loop-name">@{loop.name}</span>
        {loop.role && <span className="team-loop-role">{loop.role}</span>}
      </div>
      <div className="team-loop-handoffs">{handoffLine(loop)}</div>
      {loop.skills.length > 0 && (
        <div className="team-loop-skills">
          <div className="team-loop-skills-label">Skills it was built to use</div>
          <SkillHints skills={loop.skills} leftOut={leftOut} onToggle={onToggle} />
        </div>
      )}
      <details className="team-loop-mission">
        <summary>Mission</summary>
        <pre>{loop.mission}</pre>
      </details>
    </li>
  )
}
