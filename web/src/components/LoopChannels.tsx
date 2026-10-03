import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Channel, type LoopView, type Room } from '../api'
import { channelLabel, FLEET_CHANNEL } from '../channels'
import { loopSurface } from '../slack'

// A channel as a pane of a loop's page shows it (#549): what the channel is
// for, then each loop in it, this one first, with the Telegram group its own
// bot carries the channel in. Rooms are a loop's own (ADR-0038): one loop
// binding a group says nothing about another's bot, and a loop's posts reach
// Telegram only through its own, so the line is per loop rather than one
// group for the channel. Binding is the side panel's, on each loop's page.
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
    enabled: surface === 'telegram',
  })
  const room = rooms.data?.find((r) => r.channel === channel)
  // One outcome said one way, posts staying on the hub, with its reason:
  // in the warning colour where binding a Telegram group would change it,
  // linked to where that is done, and muted for a loop with no surface,
  // which is a choice rather than a fault.
  const hubOnly = 'posts stay on the hub: '
  let where: ReactNode
  if (surface === 'telegram') {
    where = room ? (
      <span title={room.room_id}>Telegram group {room.title || room.room_id}</span>
    ) : (
      <a className="chan-warn" href={self ? '#telegram-groups' : `/loops/${name}#telegram-groups`}>
        {hubOnly}no Telegram group carries #{channel}
      </a>
    )
  } else if (surface === 'slack') {
    where = <span className="chan-quiet">on Slack</span>
  } else {
    where = <span className="chan-quiet">{hubOnly}no surface</span>
  }
  return (
    <li>
      <Link to={`/loops/${name}`}>@{name}</Link>
      <span className="chan-sep"> · </span>
      {rooms.isSuccess || surface !== 'telegram' ? where : null}
    </li>
  )
}

// The loop's Telegram groups, in the side panel. While the loop has no
// fleet channel group, the first group that messages the bot binds to the
// fleet channel by itself; after that a new group shows up here unbound, and
// its messages are not read until it is bound to one of the loop's channels,
// so the unbound ones come first: they are what asks for the operator. The
// bound ones follow with the channel each carries, to be forgotten. Absent
// on a hub from before rooms, which has no list to read.
export function TelegramGroups({ loop }: { loop: LoopView }) {
  const rooms = useLoopRooms(loop)
  const channels = useQuery({ queryKey: ['channels'], queryFn: api.channels, retry: false })
  if (!rooms) return null
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
    <div className="loop-chans">
      {unbound.map((room) => (
        <UnboundRoom key={room.room_id} loop={loop} room={room} rooms={rooms} channels={free} />
      ))}
      {unbound.length > 0 && (
        <div className="chans-note">Not read until it carries one of @{loop.name}'s channels.</div>
      )}
      {rooms.length === 0 ? (
        <div className="chans-note">
          None yet. Add {bot} to a Telegram group: the first one carries the fleet channel, and any later one
          shows up here for a channel to take.
        </div>
      ) : (
        unbound.length === 0 && <div className="chans-note">Every Telegram group carries a channel.</div>
      )}
      {bound.map((room) => (
        <BoundRoom key={room.room_id} loop={loop} room={room} rooms={rooms} />
      ))}
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
    </div>
  )
}

// A loop's rooms, for a loop on Telegram; undefined for any other, and on a
// hub from before rooms.
function useLoopRooms(loop: LoopView): Room[] | undefined {
  const telegram = loopSurface(loop) === 'telegram'
  const rooms = useQuery({
    queryKey: ['rooms', loop.name],
    queryFn: () => api.rooms(loop.name),
    retry: false,
    enabled: telegram,
  })
  return telegram ? rooms.data : undefined
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
      api.bindRoom(loop.name, { surface: 'telegram', ...body }),
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
  const name = room.title || 'untitled group'
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
          <option value="">{channels.length ? 'Choose a channel…' : 'Every channel has a group'}</option>
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
        <span className="room-name">{room.title || 'untitled group'}</span>
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
  const name = room.title || 'untitled group'
  const confirmForget = () => {
    const what = !room.channel
      ? 'It is no longer listed.'
      : room.channel === FLEET_CHANNEL
        ? `@${loop.name} stops reading and posting there.`
        : `@${loop.name} stops reading and posting there, and #${room.channel} has no group until another is bound.`
    // With no fleet channel room left, the next group to write binds to the
    // fleet channel by itself (bridge.go roomChannel), this one included.
    const fleetRoomStays = rooms.some(
      (other) => other.channel === FLEET_CHANNEL && other.room_id !== room.room_id,
    )
    const after = fleetRoomStays
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
