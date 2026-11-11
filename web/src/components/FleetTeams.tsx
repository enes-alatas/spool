import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type LoopView, type Team } from '../api'
import { channelRefusal, descriptionLength, homeChannelOptions, loopsNotInTeam } from '../channels'
import { loginOptions } from '../claudeLogins'
import { inTurn } from '../format'
import {
  loopsToAdd,
  moveTargets,
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
// adds a loop to a team, moves a loop between the cards, edits or deletes a
// team, and makes a new one from here; a member sees the same cards without
// those controls.

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
  const moves = useLoopMoves(teams)
  return (
    <>
      <div className={`team-cards${moves.dragging ? ' dragging' : ''}`}>
        {teamGroups(teams, loops).map((group) => (
          <TeamCard
            key={group.key || '-'}
            group={group}
            loops={loops}
            spentToday={spentToday}
            manage={manage}
            hidden={focused?.name}
            onOpen={(name, el) => setFocused({ name, from: el.getBoundingClientRect(), back: el })}
            moves={moves}
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

// Moving a loop between the cards (#1485), by dragging its circle onto
// another card or from its Move to… menu: one PATCH of its team, '' for
// none, as the Add loop pick-list sends (ADR-0066). While the hub has the
// move, the loop's place on its old card stays held by an empty circle; a
// refusal leaves it there and is said on the card it was meant for.
type LoopMoves = ReturnType<typeof useLoopMoves>

function useLoopMoves(teams: Team[]) {
  const qc = useQueryClient()
  // The loop being dragged and the card it left, and the card under it. The
  // ref is what the cards' drag events read: it is set as the drag starts,
  // where the state that draws it is a frame behind.
  const [dragging, setDragging] = useState<{ loop: string; from: string }>()
  const held = useRef<{ loop: string; from: string }>()
  const [over, setOver] = useState<string>()
  // The loop whose move the hub has not answered yet.
  const [pending, setPending] = useState<string>()
  const [refusal, setRefusal] = useState<{ card: string; text: string }>()
  // A move from the menu gives the focus back to the loop's circle on the
  // card it ends on, once that card draws it, or the keyboard is left at
  // the top of the page.
  const [focusOn, setFocusOn] = useState<{ loop: string; card: string }>()

  const move = async (loop: string, from: string, to: string, refocus = false) => {
    // Back onto the card it came from: nothing to ask.
    if (from === to) return
    setRefusal(undefined)
    setFocusOn(undefined)
    setPending(loop)
    let card = from
    try {
      await api.patchLoop(loop, { team: to })
      // Held until the cards are redrawn from the hub, so the loop is never
      // in neither place nor in both.
      await Promise.all([
        qc.invalidateQueries({ queryKey: ['teams'] }),
        qc.invalidateQueries({ queryKey: ['loops'] }),
      ])
      void qc.invalidateQueries({ queryKey: ['loop'] })
      card = to
    } catch (err) {
      setRefusal({ card: to, text: `${loop} stays in ${from || 'no team'}: ${errorText(err)}` })
    } finally {
      setPending(undefined)
      if (refocus) setFocusOn({ loop, card })
    }
  }

  return {
    // With no team there is no other card to move to.
    possible: teams.length > 0,
    targets: (from: string) => moveTargets(teams, from),
    dragging,
    over,
    pending,
    refusal,
    move,
    focusOn,
    focused: () => setFocusOn(undefined),
    held: () => held.current,
    start: (loop: string, from: string) => {
      held.current = { loop, from }
      setRefusal(undefined)
      // A frame late: the browser takes the drag's picture of the circle as
      // the drag starts, and a circle already hidden would give it nothing.
      // A drag that ends within that frame has cleared the ref, so nothing
      // is drawn.
      requestAnimationFrame(() => {
        if (held.current?.loop === loop) setDragging({ loop, from })
      })
    },
    end: () => {
      held.current = undefined
      setDragging(undefined)
      setOver(undefined)
    },
    setOver,
  }
}

// A loop's Move to… menu (#1485): every card it could go to, and the one it
// is on, shown and not offered. The arrow keys walk it, Escape or a click
// elsewhere closes it.
function MoveMenu({
  loop,
  targets,
  onPick,
  onClose,
}: {
  loop: string
  targets: { key: string; label: string; current: boolean }[]
  onPick: (key: string) => void
  onClose: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const items = () => [...(ref.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])]
  // The latest onClose, read when it fires: the page redraws as the fleet
  // streams in, and the effect below must run once, or each redraw would
  // pull the focus back to the first item.
  const close = useRef(onClose)
  useEffect(() => {
    close.current = onClose
  })
  useEffect(() => {
    ref.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
    const away = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) close.current()
    }
    document.addEventListener('pointerdown', away)
    return () => document.removeEventListener('pointerdown', away)
  }, [])
  const keys = (e: React.KeyboardEvent) => {
    const all = items()
    const at = all.indexOf(document.activeElement as HTMLButtonElement)
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const step = e.key === 'ArrowDown' ? 1 : -1
      all[(at + step + all.length) % all.length]?.focus()
    } else if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault()
      all[e.key === 'Home' ? 0 : all.length - 1]?.focus()
    } else if (e.key === 'Escape' || e.key === 'Tab') {
      e.preventDefault()
      onClose()
    }
  }
  return (
    <div ref={ref} className="move-menu" role="menu" aria-label={`Move ${loop} to`} onKeyDown={keys}>
      <div className="move-menu-title" aria-hidden>
        Move {loop} to…
      </div>
      {targets.map((target) => (
        <button
          key={target.key || '-'}
          type="button"
          role="menuitem"
          disabled={target.current}
          onClick={() => onPick(target.key)}
        >
          {target.label}
          {target.current && ' · now'}
        </button>
      ))}
    </div>
  )
}

function TeamCard({
  group,
  loops,
  spentToday,
  manage,
  hidden,
  onOpen,
  moves,
}: {
  group: TeamGroup
  loops: LoopView[]
  spentToday: number
  manage: boolean
  // The loop open in the middle of the page, whose place stays empty.
  hidden?: string
  onOpen: (name: string, el: HTMLElement) => void
  moves: LoopMoves
}) {
  const { team } = group
  const [editing, setEditing] = useState(false)
  const [menuFor, setMenuFor] = useState<string>()
  const { dragging } = moves
  // Whether a loop dragged now would land here: any card but its own.
  const takes = () => {
    const drag = moves.held()
    return !!drag && drag.from !== group.key
  }
  const over = !!dragging && dragging.from !== group.key && moves.over === group.key
  const label = team ? team.name : 'no team'
  const slots = useRef(new Map<string, HTMLElement>())
  const faults = group.loops.filter(needsYou).length
  const working = group.loops.filter((loop) => inTurn(loop.state)).length
  const orb = (name: string) => {
    const el = slots.current.get(name)?.firstElementChild
    return el instanceof HTMLElement ? el : undefined
  }
  // The moved loop's circle, once this card is the one drawing it: until the
  // cards are redrawn from the hub it is still on the card it left.
  const { focusOn, focused } = moves
  useEffect(() => {
    if (focusOn?.card !== group.key) return
    const el = orb(focusOn.loop)
    if (!el) return
    el.focus({ preventScroll: true })
    focused()
  })
  return (
    <section
      className={`team-card${team ? '' : ' loose'}${faults > 0 ? ' alert' : ''}${over ? ' drop-target' : ''}`}
      onDragOver={(e) => {
        if (!takes()) return
        e.preventDefault()
        e.dataTransfer.dropEffect = 'move'
        if (!over) moves.setOver(group.key)
      }}
      onDragLeave={(e) => {
        // Leaving for one of its own children is still over the card.
        if (over && !e.currentTarget.contains(e.relatedTarget as Node | null)) moves.setOver(undefined)
      }}
      onDrop={(e) => {
        const drag = moves.held()
        if (!drag || drag.from === group.key) return
        e.preventDefault()
        moves.end()
        void moves.move(drag.loop, drag.from, group.key)
      }}
    >
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
                className={`orb-slot${dragging?.loop === loop.name || moves.pending === loop.name ? ' moving' : ''}`}
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
                    const el = orb(loop.name)
                    if (el) onOpen(loop.name, el)
                  }}
                  drag={
                    manage && moves.possible && !moves.pending
                      ? { onStart: () => moves.start(loop.name, group.key), onEnd: moves.end }
                      : undefined
                  }
                  onMenu={
                    manage && moves.possible && !moves.pending ? () => setMenuFor(loop.name) : undefined
                  }
                />
                {menuFor === loop.name && (
                  <MoveMenu
                    loop={loop.name}
                    targets={moves.targets(group.key)}
                    onPick={(to) => {
                      setMenuFor(undefined)
                      void moves.move(loop.name, group.key, to, true)
                    }}
                    onClose={() => {
                      setMenuFor(undefined)
                      orb(loop.name)?.focus({ preventScroll: true })
                    }}
                  />
                )}
              </span>
            ))}
            {manage && (team ? <AddLoop team={team} loops={loops} /> : <NewLoopOrb />)}
          </div>
          {over && dragging && (
            <p className="team-card-drop">
              Drop to move {dragging.loop} to {label}
            </p>
          )}
          {moves.refusal?.card === group.key && (
            <div className="form-error" role="alert">
              {moves.refusal.text}
            </div>
          )}
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
