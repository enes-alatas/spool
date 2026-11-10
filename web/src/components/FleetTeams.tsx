import { useRef, useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type LoopView, type Team } from '../api'
import { channelRefusal, descriptionLength, homeChannelOptions, loopsNotInTeam } from '../channels'
import { loginOptions } from '../claudeLogins'
import { inTurn } from '../format'
import {
  loopsToAdd,
  needsYou,
  TEAM_DESCRIPTION_MAX,
  TEAM_NAME_MAX,
  teamDeleteRefusal,
  teamGroups,
  validTeamName,
  type TeamGroup,
} from '../teams'
import { CharCount } from './CharCount'
import { EditIcon } from './Icons'
import { FocusedOrb, LoopOrb } from './LoopOrb'
import { StepDialog } from './StepDialog'

// The fleet as its teams (ADR-0066, #1411): a card per team with a circle
// per loop, and the loops in no team in a full-width card last. An admin
// adds a loop to a team, edits or deletes a team, and makes a new one from
// here; a member sees the same cards without those controls.

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function useRefresh() {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: ['teams'] })
    void qc.invalidateQueries({ queryKey: ['loops'] })
    void qc.invalidateQueries({ queryKey: ['loop'] })
  }
}

// What a circle's rings and halo mean, for the Fleet page's bar.
export function OrbKey() {
  return (
    <p className="orb-key">
      <span>
        <i className="key context" /> outer ring: context
      </span>
      <span>
        <i className="key share" /> inner ring: share of today's spend
      </span>
      <span>
        <i className="key halo" /> red halo: needs you
      </span>
      <span>
        <i className="key spin" /> turning: in a turn
      </span>
    </p>
  )
}

export function FleetTeams({
  teams,
  loops,
  spentToday,
  manage,
}: {
  teams: Team[]
  loops: LoopView[]
  spentToday: number
  manage: boolean
}) {
  const [focused, setFocused] = useState<{ name: string; from: DOMRect; back: HTMLElement } | null>(null)
  const focusedLoop = focused && loops.find((loop) => loop.name === focused.name)
  return (
    <>
      <div className="team-cards">
        {teamGroups(teams, loops).map((group) => (
          <TeamCard
            key={group.key || '-'}
            group={group}
            loops={loops}
            spentToday={spentToday}
            manage={manage}
            hidden={focused?.name}
            onOpen={(name, el) => setFocused({ name, from: el.getBoundingClientRect(), back: el })}
          />
        ))}
        {manage && <NewTeamCard />}
      </div>
      {focused && focusedLoop && (
        <FocusedOrb
          loop={focusedLoop}
          spentToday={spentToday}
          from={focused.from}
          onClose={() => {
            // Once its place is shown again: a hidden button takes no focus.
            const back = focused.back
            setFocused(null)
            requestAnimationFrame(() => back.focus({ preventScroll: true }))
          }}
        />
      )}
    </>
  )
}

function TeamCard({
  group,
  loops,
  spentToday,
  manage,
  hidden,
  onOpen,
}: {
  group: TeamGroup
  loops: LoopView[]
  spentToday: number
  manage: boolean
  // The loop open in the middle of the page, whose place stays empty.
  hidden?: string
  onOpen: (name: string, el: HTMLElement) => void
}) {
  const { team } = group
  const [editing, setEditing] = useState(false)
  const slots = useRef(new Map<string, HTMLElement>())
  const faults = group.loops.filter(needsYou).length
  const working = group.loops.filter((loop) => inTurn(loop.state)).length
  return (
    <section className={`team-card${team ? '' : ' loose'}${faults > 0 ? ' alert' : ''}`}>
      <header className="team-card-head">
        <h2>
          {team && <span className="team-card-kind">team</span>}
          {team ? team.name : 'No team'}
        </h2>
        <span className={`team-card-pulse${faults ? ' fault' : working ? ' working' : ''}`}>
          {faults ? `${faults} need${faults === 1 ? 's' : ''} you` : working ? `${working} working` : 'quiet'}
        </span>
        {team && manage && !editing && (
          <button
            type="button"
            className="team-card-edit"
            aria-label={`Edit ${team.name}`}
            title={`Edit ${team.name}`}
            onClick={() => setEditing(true)}
          >
            <EditIcon />
          </button>
        )}
      </header>
      {team && editing ? (
        <TeamEditor team={team} onDone={() => setEditing(false)} />
      ) : (
        <>
          {/* One line; the browser's own tooltip shows the rest. */}
          <p className="team-card-desc" title={team?.description || undefined}>
            {team ? team.description || 'No description' : "Loops that aren't in a team."}
          </p>
          {/* Only a login the team names: the hub's default is every loop's
              unless something says otherwise. */}
          {team?.claude_login && <p className="team-card-login">Default team login: {team.claude_login}</p>}
          <div className="team-card-loops">
            {group.loops.map((loop) => (
              <span
                key={loop.id}
                className="orb-slot"
                style={{ visibility: hidden === loop.name ? 'hidden' : undefined }}
                ref={(el) => {
                  if (el) slots.current.set(loop.name, el)
                  else slots.current.delete(loop.name)
                }}
              >
                <LoopOrb
                  loop={loop}
                  spentToday={spentToday}
                  onOpen={() => {
                    const el = slots.current.get(loop.name)?.firstElementChild
                    if (el instanceof HTMLElement) onOpen(loop.name, el)
                  }}
                />
              </span>
            ))}
            {manage && (team ? <AddLoop team={team} loops={loops} /> : <NewLoopOrb />)}
          </div>
        </>
      )}
    </section>
  )
}

// The dashed circle at the end of a team's loops: a pick-list of the loops
// it could take, each with the team it would leave, since a loop is in one
// team at most. Choosing one moves it at once.
function AddLoop({ team, loops }: { team: Team; loops: LoopView[] }) {
  const refresh = useRefresh()
  const [error, setError] = useState('')
  const addable = loopsToAdd(team, loops)
  if (addable.length === 0) return null
  const add = async (name: string) => {
    setError('')
    try {
      await api.patchLoop(name, { team: team.name })
    } catch (err) {
      setError(errorText(err))
    }
    refresh()
  }
  return (
    <>
      <label className="orb-add" title={`Add a loop to ${team.name}`}>
        <span aria-hidden>+</span>
        <select value="" aria-label={`Add a loop to ${team.name}`} onChange={(e) => void add(e.target.value)}>
          <option value="" disabled>
            Add a loop to {team.name}
          </option>
          {addable.map((loop) => (
            <option key={loop.name} value={loop.name}>
              {loop.name}
              {loop.from && ` (from ${loop.from})`}
            </option>
          ))}
        </select>
      </label>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </>
  )
}

// In the card of loops in no team, the dashed circle makes a new loop.
function NewLoopOrb() {
  return (
    <Link to="/new" className="orb-add" title="New loop" aria-label="New loop">
      <span aria-hidden>+</span>
    </Link>
  )
}

function TeamEditor({ team, onDone }: { team: Team; onDone: () => void }) {
  const refresh = useRefresh()
  const [name, setName] = useState(team.name)
  const [description, setDescription] = useState(team.description)
  // '' is the hub's default (ADR-0066).
  const [login, setLogin] = useState(team.claude_login ?? '')
  const { data: logins } = useQuery({ queryKey: ['claude-logins'], queryFn: api.claudeLogins })
  // '' is none. Any hub channel may be picked: naming it gives it to the
  // team (ADR-0068).
  const [home, setHome] = useState(team.home_channel)
  const { data: channels } = useQuery({ queryKey: ['channels'], queryFn: api.channels, retry: false })
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const badName = !validTeamName(name)
  const tooLong = descriptionLength(description) > TEAM_DESCRIPTION_MAX

  const run = async (action: () => Promise<unknown>, refusal: (err: unknown) => string = errorText) => {
    setBusy(true)
    setError('')
    try {
      await action()
      refresh()
      onDone()
    } catch (err) {
      setError(refusal(err))
    } finally {
      setBusy(false)
    }
  }

  // A hub channel holding loops outside the team is refused naming them,
  // and said here naming them too, with what to do first.
  const homeRefusal = (err: unknown) =>
    channelRefusal(err, {
      channel: home,
      team: team.name,
      outsiders: loopsNotInTeam(channels?.find((c) => c.name === home)?.loops ?? [], team.name, loops ?? []),
    })

  const save = () =>
    run(
      () =>
        api.patchTeam(team.name, {
          ...(name !== team.name && { name }),
          description: description.trim(),
          // Only when it changes, so a hub too old for it is asked what it was.
          ...(login !== (team.claude_login ?? '') && { claude_login: login }),
          ...(home !== team.home_channel && { home_channel: home }),
        }),
      homeRefusal,
    )

  // Said before asking the hub, which refuses a team with loops the same way.
  const remove = () => {
    const refusal = teamDeleteRefusal(team)
    if (refusal) setError(refusal)
    else if (confirm(`Delete the team ${team.name}?`)) void run(() => api.deleteTeam(team.name))
  }

  return (
    <TeamForm
      id={`team-${team.name}`}
      name={name}
      description={description}
      onName={setName}
      onDescription={setDescription}
      badName={badName}
      tooLong={tooLong}
      error={error}
      login={
        // A hub too old for team logins sends none, and offers no picker.
        logins && team.claude_login !== undefined
          ? { value: login, onChange: setLogin, options: loginOptions(logins.logins) }
          : undefined
      }
      home={
        channels ? { value: home, onChange: setHome, options: homeChannelOptions(team, channels) } : undefined
      }
    >
      <button type="button" className="btn primary sm" onClick={save} disabled={busy || badName || tooLong}>
        {busy ? 'Saving…' : 'Save'}
      </button>
      <button type="button" className="btn sm" onClick={onDone}>
        Cancel
      </button>
      <button type="button" className="btn sm danger team-form-delete" onClick={remove} disabled={busy}>
        Delete team
      </button>
    </TeamForm>
  )
}

// The dashed card after the teams, which opens the New team dialog.
function NewTeamCard() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className="team-card team-card-new" onClick={() => setOpen(true)}>
        <span aria-hidden>+</span> New team
      </button>
      {open && <NewTeamDialog onClose={() => setOpen(false)} />}
    </>
  )
}

// A team made from its name and description, in a popup over the page,
// from the Teams view's card or the List view's button (#1484). Typed work
// is kept from a stray backdrop click or Escape; Cancel and the ✕ still
// close it. `onCreated` hears the new team's name once the hub has it.
export function NewTeamDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated?: (name: string) => void
}) {
  const refresh = useRefresh()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  // Said once a name is typed, not before: an empty field isn't a mistake.
  const badName = name !== '' && !validTeamName(name)
  const tooLong = descriptionLength(description) > TEAM_DESCRIPTION_MAX

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      await api.addTeam({ name, description: description.trim() })
      refresh()
      onCreated?.(name)
      onClose()
    } catch (err) {
      setError(errorText(err))
      setBusy(false)
    }
  }

  return (
    <StepDialog
      title="New team"
      className="new-team-dialog"
      dismissible={name === '' && description === ''}
      busy={busy}
      onClose={onClose}
    >
      <TeamForm
        id="team-new"
        name={name}
        description={description}
        onName={(value) => {
          setName(value)
          setError('')
        }}
        onDescription={setDescription}
        badName={badName}
        tooLong={tooLong}
        error={error}
      >
        <button
          type="button"
          className="btn primary sm"
          onClick={create}
          disabled={busy || !name || badName || tooLong}
        >
          {busy ? 'Creating…' : 'Create'}
        </button>
        <button type="button" className="btn sm" onClick={onClose} disabled={busy}>
          Cancel
        </button>
      </TeamForm>
    </StepDialog>
  )
}

// A pick-list a team form offers, by value and label.
type Picker = {
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
}

function TeamForm({
  id,
  name,
  description,
  onName,
  onDescription,
  badName,
  tooLong,
  error,
  login,
  home,
  children,
}: {
  id: string
  name: string
  description: string
  onName: (name: string) => void
  onDescription: (description: string) => void
  badName: boolean
  tooLong: boolean
  error: string
  // The team's Claude login picker, on an edit.
  login?: Picker
  // The team's home channel picker, on an edit.
  home?: Picker
  children: React.ReactNode
}) {
  return (
    <div className="team-form">
      <label htmlFor={`${id}-name`}>Name</label>
      <input
        id={`${id}-name`}
        className="mono"
        value={name}
        maxLength={TEAM_NAME_MAX}
        onChange={(e) => onName(e.target.value)}
        aria-invalid={badName}
        aria-describedby={`${id}-rule`}
        autoComplete="off"
        spellCheck={false}
        autoFocus
      />
      <div id={`${id}-rule`} className={`team-form-note${badName ? ' bad' : ''}`}>
        Lowercase letters, digits, dashes and underscores, 2 to 32, as a loop's name.
      </div>
      <div className="team-form-label">
        <label htmlFor={`${id}-description`}>Description</label>
        <CharCount used={descriptionLength(description)} max={TEAM_DESCRIPTION_MAX} />
      </div>
      <input
        id={`${id}-description`}
        value={description}
        onChange={(e) => onDescription(e.target.value)}
        placeholder="What it is for (optional)"
        aria-invalid={tooLong}
      />
      {home && (
        <>
          <label htmlFor={`${id}-home`}>Home channel</label>
          <select id={`${id}-home`} value={home.value} onChange={(e) => home.onChange(e.target.value)}>
            {home.options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <div className="team-form-note">
            Every loop in the team joins it. A hub channel becomes the team's, keeping its history, and must
            hold only the team's loops.
          </div>
        </>
      )}
      {login && (
        <>
          <label htmlFor={`${id}-login`}>Default team login</label>
          <select id={`${id}-login`} value={login.value} onChange={(e) => login.onChange(e.target.value)}>
            {login.options.map((o) => (
              // The default is named by naming none.
              <option key={o.value} value={o.value === 'default' ? '' : o.value}>
                {o.value === 'default' ? "the hub's default" : o.label}
              </option>
            ))}
          </select>
          <div className="team-form-note">
            The Claude login the team's loops run on when they name none. A loop can still name its own on its
            page. Cloud loops keep their own, or the hub's default.
          </div>
        </>
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="controls">{children}</div>
    </div>
  )
}
