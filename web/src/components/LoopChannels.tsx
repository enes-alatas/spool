import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Channel, type LoopView, type Room } from '../api'
import { channelLabel, FLEET_CHANNEL } from '../channels'
import { loopSurface } from '../slack'

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

// The loop's rooms, a side panel of its page: its Telegram groups or its Slack
// channels. While the loop has no fleet channel room, the first room an
// allowed sender writes in binds to the fleet channel by itself; after that
// a new one shows up here unbound, and its messages are not read until it is
// bound to one of the loop's channels, so the unbound ones come first: they
// are what asks for the operator. The bound ones follow with the channel each
// carries, to be forgotten. Absent on a hub from before rooms, which has no
// list to read.
export function LoopRooms({ loop }: { loop: LoopView }) {
  const rooms = useLoopRooms(loop)
  const channels = useQuery({ queryKey: ['channels'], queryFn: api.channels, retry: false })
  const surface = loopSurface(loop)
  if (!rooms || surface === '') return null
  const words = ROOM_WORDS[surface]
  const unbound = rooms.filter((room) => !room.channel)
  const bound = rooms.filter((room) => room.channel)
  // The pick-list offers the loop's channels no group of its carries yet:
  // the hub refuses a channel the loop is not in, and a loop has one group
  // per channel.
  const free = (channels.data ?? [])
    .filter((channel) => channel.loops.includes(loop.name))
    .map((channel) => channel.name)
    .filter((name) => !rooms.some((room) => room.channel === name))
  const bot = loop.tg_bot_username ? `@${loop.tg_bot_username}` : 'the bot'
  return (
    <div className="side-panel" id={ROOMS_ANCHOR}>
      <h3>{words.title}</h3>
      <div className="loop-chans">
        {unbound.map((room) => (
          <UnboundRoom key={room.room_id} loop={loop} room={room} rooms={rooms} channels={free} />
        ))}
        {unbound.length > 0 && (
          <div className="chans-note">Not read until it carries one of @{loop.name}'s channels.</div>
        )}
        {rooms.length === 0 ? (
          <div className="chans-note">
            {surface === 'slack' ? (
              <>
                None yet. Invite the app to a Slack channel: the first one an allowed sender writes in carries
                the fleet channel, and any later one shows up here for a channel to take.
              </>
            ) : (
              <>
                None yet. Add {bot} to a Telegram group: the first one carries the fleet channel, and any
                later one shows up here for a channel to take.
              </>
            )}
          </div>
        ) : (
          unbound.length === 0 && <div className="chans-note">Every {words.kind} carries a channel.</div>
        )}
        {bound.map((room) => (
          <BoundRoom key={room.room_id} loop={loop} room={room} rooms={rooms} />
        ))}
        {surface === 'slack' ? (
          <div className="chans-note">
            A Slack channel shows up here once an allowed sender writes in it with the app invited. A pending
            sender's message records nothing: allow them on Access.
          </div>
        ) : (
          <TelegramNotes loop={loop} bot={bot} />
        )}
      </div>
    </div>
  )
}

// What Telegram's platform limits mean for a group, said where they bite.
// Slack's are on the loop's Slack status, beside the reaction and poll
// hints that need its app.
function TelegramNotes({ loop, bot }: { loop: LoopView; bot: string }) {
  return (
    <>
      <div className="chans-note">
        A group shows up here once someone @mentions {bot} in it: by default, Telegram passes a bot only the
        messages that mention it or reply to it.
      </div>
      {/* ADR-0040: the platform's limits on reactions, said where they bite */}
      <div className="chans-note">
        Reactions: in a group, {bot} hears them only as an administrator. It sets one reaction per message, so
        a loop's second reaction on a message replaces its first.
      </div>
      {/* ADR-0041: Telegram draws its own count, which cannot hold a loop's vote */}
      <div className="chans-note">
        Polls: Telegram's count shows only people's votes. Loops' votes are in the tally here, which is the
        result @{loop.name} is told.
      </div>
    </>
  )
}

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
    // binding the fleet channel's room is the loop's group binding too
    qc.invalidateQueries({ queryKey: ['loop', loop.name] })
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

function UnboundRoom({
  loop,
  room,
  rooms,
  channels,
}: {
  loop: LoopView
  room: Room
  rooms: Room[]
  channels: string[]
}) {
  const [channel, setChannel] = useState('')
  const { bind, forget, error, busy } = useRoomChange(loop, () => setChannel(''))
  const surface = roomSurface(room)
  const name = roomName(surface, room)
  return (
    <div className="chan-room">
      <div className="chan-line">
        <span className="room-name unbound">{name}</span>
        <ForgetButton
          loop={loop}
          room={room}
          rooms={rooms}
          busy={busy}
          onForget={() => forget.mutate(room)}
        />
      </div>
      <span className="room-id">{room.room_id}</span>
      <div className="chan-actions">
        <select
          className="panel-select"
          aria-label={`Channel for ${name}`}
          value={channel}
          disabled={busy}
          onChange={(e) => setChannel(e.target.value)}
        >
          <option value="">
            {channels.length ? 'Choose a channel…' : `Every channel has a ${ROOM_WORDS[surface].one}`}
          </option>
          {channels.map((c) => (
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
      <ErrorLine error={error} />
    </div>
  )
}

function BoundRoom({ loop, room, rooms }: { loop: LoopView; room: Room; rooms: Room[] }) {
  const { forget, error, busy } = useRoomChange(loop)
  return (
    <div className="chan-room">
      <div className="chan-line">
        <span className="room-name">{roomName(roomSurface(room), room)}</span>
        <span className="room-carries">{channelLabel(room.channel)}</span>
        <ForgetButton
          loop={loop}
          room={room}
          rooms={rooms}
          busy={busy}
          onForget={() => forget.mutate(room)}
        />
      </div>
      <span className="room-id">{room.room_id}</span>
      <ErrorLine error={error} />
    </div>
  )
}

function ForgetButton({
  loop,
  room,
  rooms,
  busy,
  onForget,
}: {
  loop: LoopView
  room: Room
  rooms: Room[]
  busy: boolean
  onForget: () => void
}) {
  const surface = roomSurface(room)
  const name = roomName(surface, room)
  const one = ROOM_WORDS[surface].one
  const confirmForget = () => {
    const what = !room.channel
      ? 'It is no longer listed.'
      : room.channel === FLEET_CHANNEL
        ? `@${loop.name} stops reading and posting there.`
        : `@${loop.name} stops reading and posting there, and #${room.channel} has no ${one} until another is bound.`
    // With no fleet channel room left, the next room to write binds to the
    // fleet channel by itself (roomChannel in either surface's inbound),
    // this one included.
    const fleetRoomStays = rooms.some(
      (other) => other.channel === FLEET_CHANNEL && other.room_id !== room.room_id,
    )
    const after =
      surface === 'slack'
        ? fleetRoomStays
          ? 'If an allowed sender writes there again, it comes back unbound.'
          : "The next Slack channel an allowed sender writes in, this one included, becomes the fleet channel's room by itself."
        : fleetRoomStays
          ? 'If the group messages the bot again, it comes back unbound.'
          : "The next group to message the bot, this one included, becomes the fleet channel's room by itself."
    if (confirm(`Forget ${name}? ${what} ${after}`)) onForget()
  }
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
