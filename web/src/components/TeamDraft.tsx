import { useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api, type PromptsChatTeam, type PromptsChatTeamLoop } from '../api'
import { descriptionLength } from '../channels'
import { handoffLine } from '../team'
import { TEAM_DESCRIPTION_MAX, validTeamName } from '../teams'
import { TeamFields } from './TeamFields'
import { SkillHints } from './PromptsChatImport'
import { skillsToInstall } from '../skills'

const NONE: ReadonlySet<string> = new Set()

// A team read from prompts.chat (#1266, ADR-0063), shown on New loop in
// place of the form and in the New team dialog (#1559): what Create makes,
// read before anything exists. Create makes the team, its home channel and
// every loop in it, or none of them; `onCreated` hears the team's name.
// The team's name and description are the operator's to edit. The loops'
// names and the channel's are the draft's and are not: each mission names
// its teammates and its channel by them, so a rename would leave the
// missions pointing at what isn't there. A loop is renamed on its own page
// after.
export function TeamDraft({
  team,
  onCancel,
  onCreated,
  onBusy,
}: {
  team: PromptsChatTeam
  onCancel: () => void
  onCreated: (name: string) => void
  // Hears a create start and fail, so a dialog holding it stays open.
  onBusy?: (busy: boolean) => void
}) {
  const qc = useQueryClient()
  const [name, setName] = useState(team.channel)
  const [description, setDescription] = useState(team.description)
  const [busy, setBusyState] = useState(false)
  const setBusy = (next: boolean) => {
    setBusyState(next)
    onBusy?.(next)
  }
  const [error, setError] = useState('')
  const badName = !validTeamName(name)
  const tooLong = descriptionLength(description) > TEAM_DESCRIPTION_MAX
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
        team: name,
        channel: team.channel,
        description: description.trim(),
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
        qc.invalidateQueries({ queryKey: ['teams'] }),
      ])
      setBusy(false)
      onCreated(made.team)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  const count = `${team.loops.length} loop${team.loops.length === 1 ? '' : 's'}`
  return (
    <TeamPreview
      team={team}
      count={count}
      leftOut={leftOut}
      onToggle={toggle}
      head={
        <div className="team-form">
          <TeamFields
            id="team-draft"
            name={name}
            description={description}
            onName={(value) => {
              setName(value)
              setError('')
            }}
            onDescription={setDescription}
            badName={badName}
            tooLong={tooLong}
            nameLabel="Team name"
          />
        </div>
      }
    >
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="team-actions">
        <button
          type="button"
          className="btn primary"
          onClick={() => void create()}
          disabled={busy || badName || tooLong}
        >
          {busy ? 'Creating…' : `Create the team and its ${count}`}
        </button>
        <button type="button" className="btn" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </TeamPreview>
  )
}

// What Create makes, read before it does: the team as `head` says it, its
// home channel, then each loop with its role and handoffs, its mission
// folded away but one click from being read in full, since it is the bot's
// text, not the operator's.
function TeamPreview({
  team,
  count,
  leftOut,
  onToggle,
  head,
  children,
}: {
  team: PromptsChatTeam
  count: string
  leftOut: ReadonlyMap<string, ReadonlySet<string>>
  onToggle: (loop: string, dir: string) => void
  head: ReactNode
  children: ReactNode
}) {
  return (
    <section className="team-preview" aria-label="What Create makes">
      {head}
      <div className="team-channel">
        <span className="team-channel-label">Home channel</span>
        <span className="channel-name">#{team.channel}</span>
        <span className="channel-count">{count}</span>
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
