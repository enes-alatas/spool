import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Channel, type LoopView, type Room } from '../api'
import { channelLabel } from '../channels'
import { groupProgress } from '../groupGuide'
import { loopSurface } from '../slack'
import { useMay } from './Session'
import { CheckIcon } from './Icons'
import { POLL_MS } from './LinksSection'
import { StepDialog } from './StepDialog'
import { Copyable } from './Copyable'

// A channel as a pane of a loop's page shows it (#549): what the channel is
// for, then each loop in it, this one first, with the room — a Telegram
// group or a Slack channel — its own bot carries the channel in. Rooms are
// a loop's own (ADR-0038): one loop binding a room says nothing about
// another's bot, and a loop's posts reach its surface only through its own,
// so the line is per loop rather than one room for the channel. Binding is
// the side panel's, on each loop's page.
export function ChannelHead({ loop, channel }: { loop: LoopView; channel: Channel }) {
  const { data: loops } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const members = [loop.name, ...channel.loops.filter((name) => name !== loop.name)]
  const byName = new Map((loops ?? []).map((l) => [l.name, l]))
  return (
    <div className="chan-head">
      {channel.description && <div className="chan-desc">{channel.description}</div>}
      <ul className="chan-members" aria-label={`Loops in #${channel.name}`}>
        {members.map((name) => (
          <MemberLine
            key={name}
            name={name}
            member={name === loop.name ? loop : byName.get(name)}
            channel={channel.name}
            self={name === loop.name}
          />
        ))}
      </ul>
    </div>
  )
}

function MemberLine({
  name,
  member,
  channel,
  self,
}: {
  name: string
  member?: LoopView
  channel: string
  self: boolean
}) {
  const surface = member ? loopSurface(member) : ''
  const rooms = useQuery({
    queryKey: ['rooms', name],
    queryFn: () => api.rooms(name),
    retry: false,
    enabled: surface !== '',
  })
  const room = rooms.data?.find((r) => r.channel === channel)
  // One outcome said one way, posts staying on the hub, with its reason:
  // in the warning colour where binding a room would change it, linked to
  // where that is done, and muted for a loop with no surface, which is a
  // choice rather than a fault.
  const hubOnly = 'posts stay on the hub: '
  let where: ReactNode
  if (surface !== '') {
    const words = ROOM_WORDS[surface]
    where = room ? (
      <span title={room.room_id}>
        {words.kind} {roomName(surface, room)}
      </span>
    ) : (
      <a className="chan-warn" href={self ? `#${ROOMS_ANCHOR}` : `/loops/${name}#${ROOMS_ANCHOR}`}>
        {hubOnly}no {words.kind} carries #{channel}
      </a>
    )
  } else {
    where = <span className="chan-quiet">{hubOnly}no surface</span>
  }
  return (
    <li>
      <Link to={`/loops/${name}`}>@{name}</Link>
      <span className="chan-sep"> · </span>
      {rooms.isSuccess || surface === '' ? where : null}
    </li>
  )
}

// Where the loop page's rooms panel is, for a channel pane's link to it.
export const ROOMS_ANCHOR = 'rooms'

// How a surface's rooms are named, so one panel says each in its own words:
// a Slack channel is not a hub channel, so it is never "channel" alone.
const ROOM_WORDS = {
  telegram: { title: 'Telegram groups', kind: 'Telegram group', one: 'group' },
  slack: { title: 'Slack channels', kind: 'Slack channel', one: 'Slack channel' },
} as const

// A room as the surface names it: a Slack channel by its #name, or by its
// id until Slack has named it.
function roomName(surface: RoomSurface, room: Room): string {
  if (surface === 'slack') return room.title ? `#${room.title}` : room.room_id
  return room.title || 'untitled group'
}

type RoomSurface = keyof typeof ROOM_WORDS

// What a room's pick-list offers: the loop's channels no room carries yet,
// and whether the loop is in any channel at all, which is what an empty
// list then says. A loop in no channel has nothing to bind a room to.
interface RoomChoices {
  free: string[]
  joined: boolean
}

// The loop's rooms, a section of its page's Surfaces card (#849): its
// Telegram groups or its Slack channels. No room binds itself (ADR-0068):
// every new one shows up here unbound, and its messages are not read until
// it is bound to one of the loop's channels, so the unbound ones come first: they
// are what asks for the operator. The bound ones follow with the channel each
// carries, to be forgotten. Absent on a hub from before rooms, which has no
// list to read.
export function LoopRooms({ loop }: { loop: LoopView }) {
  const rooms = useLoopRooms(loop)
  const channels = useQuery({ queryKey: ['channels'], queryFn: api.channels, retry: false })
  const manage = useMay()('manage')
  const [adding, setAdding] = useState(false)
  const surface = loopSurface(loop)
  if (!rooms || surface === '') return null
  const words = ROOM_WORDS[surface]
  const unbound = rooms.filter((room) => !room.channel)
  const bound = rooms.filter((room) => room.channel)
  // The pick-list offers the loop's channels no group of its carries yet:
  // the hub refuses a channel the loop is not in, and a loop has one group
  // per channel.
  const mine = (channels.data ?? []).filter((channel) => channel.loops.includes(loop.name))
  const choices: RoomChoices = {
    free: mine.map((channel) => channel.name).filter((name) => !rooms.some((room) => room.channel === name)),
    joined: mine.length > 0,
  }
  const bot = loop.tg_bot_username ? `@${loop.tg_bot_username}` : 'the bot'
  // A Telegram loop's manager adds its bot to a group through the steps
  // (#1319); anyone else reads how a group shows up.
  const guide = surface === 'telegram' && manage
  return (
    <section className="surface-rooms" id={ROOMS_ANCHOR}>
      <h4 className="panel-sub">{words.title}</h4>
      {guide && (
        <button className="btn sm group-add" onClick={() => setAdding(true)}>
          {rooms.length === 0 ? 'Add to a group' : 'Add to another group'}
        </button>
      )}
      {adding && (
        <GroupDialog loop={loop} rooms={rooms} channels={choices} onClose={() => setAdding(false)} />
      )}
      <div className="loop-chans">
        {unbound.map((room) => (
          <UnboundRoom key={room.room_id} loop={loop} room={room} channels={choices} />
        ))}
        {unbound.length > 0 && (
          <div className="chans-note">Not read until it carries one of @{loop.name}'s channels.</div>
        )}
        {rooms.length === 0
          ? !guide && (
              <div className="chans-note">
                {surface === 'slack' ? (
                  <>
                    None yet. Invite the app to a Slack channel: once an allowed sender writes in it, it shows
                    up here for you to pick the channel it carries.
                  </>
                ) : (
                  <>
                    None yet. Add {bot} to a Telegram group: it shows up here for you to pick the channel it
                    carries.
                  </>
                )}
              </div>
            )
          : unbound.length === 0 && <div className="chans-note">Every {words.kind} carries a channel.</div>}
        {bound.map((room) => (
          <BoundRoom key={room.room_id} loop={loop} room={room} />
        ))}
        {surface === 'slack' ? (
          <div className="chans-note">
            A Slack channel shows up here once someone allowed to reach the loops writes in it with the app
            invited: a linked user, or someone allowed by an old pairing code. A stranger's message records
            nothing: they link from Settings → Account first.
          </div>
        ) : (
          <a className="panel-link" href={TELEGRAM_DOCS} target="_blank" rel="noreferrer">
            How groups, reactions and polls work
          </a>
        )}
      </div>
    </section>
  )
}

// How Telegram groups, reactions and polls behave, said once in the docs
// rather than under every loop's groups (#849): how a group shows up, and
// the platform's limits on reactions (ADR-0040) and polls (ADR-0041).
const TELEGRAM_DOCS = 'https://github.com/enes-alatas/spool/blob/main/docs/TELEGRAM.md'

// A loop's rooms, for a loop with a surface; undefined for one without, and
// on a hub from before rooms.
function useLoopRooms(loop: LoopView): Room[] | undefined {
  const surfaced = loopSurface(loop) !== ''
  const rooms = useQuery({
    queryKey: ['rooms', loop.name],
    queryFn: () => api.rooms(loop.name),
    retry: false,
    enabled: surfaced,
  })
  return surfaced ? rooms.data : undefined
}

function useRoomChange(loop: LoopView, onDone?: () => void) {
  const qc = useQueryClient()
  const [error, setError] = useState('')
  const done = () => {
    setError('')
    qc.invalidateQueries({ queryKey: ['rooms', loop.name] })
    onDone?.()
  }
  const fail = (e: unknown) => setError(e instanceof Error ? e.message : String(e))
  const bind = useMutation({
    mutationFn: (body: { room_id: string; channel: string }) =>
      api.bindRoom(loop.name, { surface: loopSurface(loop), ...body }),
    onSuccess: done,
    onError: fail,
  })
  const forget = useMutation({
    mutationFn: (room: Room) => api.forgetRoom(loop.name, room.surface, room.room_id),
    onSuccess: done,
    onError: fail,
  })
  return { bind, forget, error, busy: bind.isPending || forget.isPending }
}

function ErrorLine({ error }: { error: string }) {
  if (!error) return null
  return (
    <div className="form-error" role="alert">
      {error}
    </div>
  )
}

function UnboundRoom({ loop, room, channels }: { loop: LoopView; room: Room; channels: RoomChoices }) {
  const [channel, setChannel] = useState('')
  const { bind, forget, error, busy } = useRoomChange(loop, () => setChannel(''))
  const manage = useMay()('manage')
  const surface = roomSurface(room)
  const name = roomName(surface, room)
  return (
    <div className="chan-room">
      <div className="chan-line">
        <span className="room-name unbound">{name}</span>
        <ForgetButton loop={loop} room={room} busy={busy} onForget={() => forget.mutate(room)} />
      </div>
      <span className="room-id">{room.room_id}</span>
      {manage && (
        <div className="chan-actions">
          <select
            className="panel-select"
            aria-label={`Channel for ${name}`}
            value={channel}
            disabled={busy}
            onChange={(e) => setChannel(e.target.value)}
          >
            <option value="">
              {channels.free.length
                ? 'Choose a channel…'
                : channels.joined
                  ? `Every channel has a ${ROOM_WORDS[surface].one}`
                  : `@${loop.name} is in no channel`}
            </option>
            {channels.free.map((c) => (
              <option key={c} value={c}>
                {channelLabel(c)}
              </option>
            ))}
          </select>
          <button
            className="btn sm"
            disabled={busy || !channel}
            onClick={() => bind.mutate({ room_id: room.room_id, channel })}
          >
            Bind
          </button>
        </div>
      )}
      <ErrorLine error={error} />
    </div>
  )
}

function BoundRoom({ loop, room }: { loop: LoopView; room: Room }) {
  const { forget, error, busy } = useRoomChange(loop)
  return (
    <div className="chan-room">
      <div className="chan-line">
        <span className="room-name">{roomName(roomSurface(room), room)}</span>
        <span className="room-carries">{channelLabel(room.channel)}</span>
        <ForgetButton loop={loop} room={room} busy={busy} onForget={() => forget.mutate(room)} />
      </div>
      <span className="room-id">{room.room_id}</span>
      <ErrorLine error={error} />
    </div>
  )
}

function ForgetButton({
  loop,
  room,
  busy,
  onForget,
}: {
  loop: LoopView
  room: Room
  busy: boolean
  onForget: () => void
}) {
  const manage = useMay()('manage')
  const surface = roomSurface(room)
  const name = roomName(surface, room)
  const one = ROOM_WORDS[surface].one
  const confirmForget = () => {
    const what = !room.channel
      ? 'It is no longer listed.'
      : `@${loop.name} stops reading and posting there, and #${room.channel} has no ${one} until another is bound.`
    // No room binds itself (ADR-0068), so a forgotten one that writes again
    // waits for the operator like any new one.
    const after =
      surface === 'slack'
        ? 'If an allowed sender writes there again, it comes back unbound.'
        : 'If the group messages the bot again, it comes back unbound.'
    if (confirm(`Forget ${name}? ${what} ${after}`)) onForget()
  }
  if (!manage) return null
  return (
    <button className="btn sm room-forget" disabled={busy} onClick={confirmForget}>
      Forget
    </button>
  )
}

// The surface a room is on, as the panel words it; a surface this build
// does not know reads as Telegram's, whose rooms came first.
function roomSurface(room: Room): RoomSurface {
  return room.surface === 'slack' ? 'slack' : 'telegram'
}

// Adding a loop's bot to a Telegram group, step by step (#1319): add the
// bot, then send /spool_status there from a linked account, since the hub
// learns of a group only from a message an allowed sender writes in it
// (ADR-0051). The steps wait on the group live. Every group arrives unbound
// (ADR-0068) and is bound here, from a pick-list of the loop's channels.
function GroupSteps({ loop, rooms, channels }: { loop: LoopView; rooms: Room[]; channels: RoomChoices }) {
  // The groups known when the steps opened: the one that arrives is the
  // first not among them.
  const [known] = useState(() => new Set(rooms.map((room) => room.room_id)))
  // rooms_changed refreshes the list on the loop page; a poll covers a
  // stream that dropped while the operator was in Telegram.
  useQuery({
    queryKey: ['rooms', loop.name],
    queryFn: () => api.rooms(loop.name),
    refetchInterval: POLL_MS,
  })
  const { arrived, done } = groupProgress(known, rooms)
  const bot = loop.tg_bot_username ? `@${loop.tg_bot_username}` : `@${loop.name}'s bot`
  // Addressed to the bot: with privacy on, Telegram's default, a bot gets a
  // plain command only if it was the last bot to post in the group, and
  // always gets one that names it. The hub reads either.
  const command = loop.tg_bot_username ? `/spool_status@${loop.tg_bot_username}` : '/spool_status'
  const steps = [
    <>
      In Telegram, add <strong>{bot}</strong> to the group.
    </>,
    <>
      Send <Copyable text={command} /> there, from an account linked to Spool.
    </>,
    <>Pick the channel it carries, one of @{loop.name}'s.</>,
  ]
  // Until a group arrives, the first two steps are the operator's at once.
  const now = (i: number) => (done === 0 ? i < 2 : i === done)
  return (
    <>
      <ol className="link-steps">
        {steps.map((text, i) => (
          <li key={i} className={i < done ? 'done' : now(i) ? 'now' : ''}>
            <span className="link-step-mark" aria-hidden>
              {i < done ? <CheckIcon size={12} /> : i + 1}
            </span>
            <span>{text}</span>
          </li>
        ))}
      </ol>
      {arrived && !arrived.channel && (
        <div className="loop-chans">
          <UnboundRoom loop={loop} room={arrived} channels={channels} />
        </div>
      )}
      {arrived?.channel ? (
        <p className="step-dialog-state" aria-live="polite">
          <span className="ok">
            <CheckIcon size={14} /> {roomName('telegram', arrived)} carries {channelLabel(arrived.channel)}.
          </span>
        </p>
      ) : (
        !arrived && (
          <p className="step-dialog-state group-wait" aria-live="polite">
            <span className="group-wait-dot" aria-hidden />
            Waiting for the group…
            <span className="hint">
              Nothing yet? Only a linked account's message counts: link yours under Settings → Account.
            </span>
          </p>
        )
      )}
    </>
  )
}

// The steps in a dialog, from the groups section's button.
function GroupDialog({
  loop,
  rooms,
  channels,
  onClose,
}: {
  loop: LoopView
  rooms: Room[]
  channels: RoomChoices
  onClose: () => void
}) {
  return (
    <StepDialog title={`Add @${loop.name} to a group`} className="group-dialog" onClose={onClose}>
      <div className="form">
        <GroupSteps loop={loop} rooms={rooms} channels={channels} />
        <form method="dialog" className="pillar-actions">
          <button className="btn">Close</button>
        </form>
      </div>
    </StepDialog>
  )
}
