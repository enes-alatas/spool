import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type LoopView, type Room } from '../api'
import { FLEET_CHANNEL } from '../channels'

// The rooms a loop's bot is in, Telegram groups for now, and the channel each carries
// (#514, #515). While the loop has no fleet channel room, the first group
// that messages the bot binds to the fleet channel by itself. After that, a
// new group shows up here unbound, and its messages are not read until it is
// bound to one of the loop's channels, so an unbound row is the one that asks
// for something. Absent on a hub from before rooms, which has no list to read.
export function Rooms({ loop }: { loop: LoopView }) {
  const rooms = useQuery({
    queryKey: ['rooms', loop.name],
    queryFn: () => api.rooms(loop.name),
    retry: false,
  })
  const channels = useQuery({ queryKey: ['channels'], queryFn: api.channels, retry: false })
  if (!rooms.data) return null
  // The pick-list offers the loop's own channels: the hub refuses a channel
  // the loop is not in.
  const mine = (channels.data ?? []).filter((channel) => channel.loops.includes(loop.name)).map((c) => c.name)
  const bot = loop.tg_bot_username ? `@${loop.tg_bot_username}` : 'the bot'
  return (
    <div className="rooms">
      {rooms.data.length === 0 && (
        <div className="rooms-empty">
          None yet. Add {bot} to a Telegram group: the first group becomes the fleet channel's room, and any
          later one shows up here to bind to a channel.
        </div>
      )}
      {rooms.data.map((room) => (
        <RoomRow
          key={`${room.surface}:${room.room_id}`}
          loop={loop}
          room={room}
          rooms={rooms.data}
          channels={mine}
        />
      ))}
      <div className="rooms-note">
        A group shows up here once someone @mentions {bot} in it: by default, Telegram passes a bot only the
        messages that mention it or reply to it.
      </div>
      {/* ADR-0040: the platform's limits on reactions, said where they bite */}
      <div className="rooms-note">
        Reactions: in a group, {bot} hears them only as an administrator. It sets one reaction per message, so
        a loop's second reaction on a message replaces its first.
      </div>
    </div>
  )
}

// A channel as the pick-list names it: the fleet channel by its wire name and
// what it is, as the Channels page badges it. A row, narrower, says
// "fleet channel" alone.
function channelLabel(channel: string): string {
  return channel === FLEET_CHANNEL ? `#${channel} · fleet channel` : `#${channel}`
}

// A channel as a sentence names it.
function channelPhrase(channel: string): string {
  return channel === FLEET_CHANNEL ? 'the fleet channel' : `#${channel}`
}

// The room that carries a channel now, if another: binding is one room per
// channel per loop, so binding a second one sends that one back to unbound,
// and the row says so before it happens.
function holderOf(channel: string, rooms: Room[], except = ''): Room | undefined {
  if (!channel) return undefined
  return rooms.find((room) => room.channel === channel && room.room_id !== except)
}

function MovesNote({ holder, channel }: { holder?: Room; channel: string }) {
  if (!holder) return null
  return (
    <div className="hint">
      {holder.title || holder.room_id} carries {channelPhrase(channel)} now, and goes back to unbound.
    </div>
  )
}

function ChannelSelect({
  channels,
  value,
  onChange,
  disabled,
  label,
}: {
  channels: string[]
  value: string
  onChange: (channel: string) => void
  disabled: boolean
  label: string
}) {
  return (
    <select
      className="panel-select"
      aria-label={label}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
    >
      <option value="">{channels.length ? 'Choose a channel…' : 'The loop is in no channel'}</option>
      {channels.map((channel) => (
        <option key={channel} value={channel}>
          {channelLabel(channel)}
        </option>
      ))}
    </select>
  )
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

function RoomRow({
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
    if (confirm(`Forget ${name}? ${what} ${after}`)) {
      forget.mutate(room)
    }
  }

  return (
    <div className="room">
      <div className="room-line">
        <span className="room-title">{name}</span>
        {room.channel ? (
          <span className="room-channel">
            {room.channel === FLEET_CHANNEL ? 'fleet channel' : `#${room.channel}`}
          </span>
        ) : (
          <span className="room-unbound">unbound</span>
        )}
      </div>
      <div className="room-line">
        <span className="room-id">{room.room_id}</span>
        <button className="btn sm room-forget" disabled={busy} onClick={confirmForget}>
          Forget
        </button>
      </div>
      {!room.channel && (
        <>
          <div className="room-actions">
            <ChannelSelect
              channels={channels}
              value={channel}
              onChange={setChannel}
              disabled={busy}
              label={`Channel for ${name}`}
            />
            <button
              className="btn sm"
              disabled={busy || !channel}
              onClick={() => bind.mutate({ room_id: room.room_id, channel })}
            >
              Bind
            </button>
          </div>
          <MovesNote holder={holderOf(channel, rooms, room.room_id)} channel={channel} />
        </>
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}
