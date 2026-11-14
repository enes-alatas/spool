import { type ReactNode } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Channel, type LoopView, type Room } from '../api'
import { channelLabel } from '../channels'
import { loopSurface } from '../slack'
import { useMay } from './Session'

// A channel as a pane of a loop's page shows it (#549): what the channel is
// for, then each loop in it, this one first, with the room — a Telegram
// group or a Slack channel — its own bot carries the channel in. A loop's
// posts reach its surface only through its own bot, so the line is per
// loop: one member's bot may be in the channel's chat while another's isn't
// yet. The chat is bound on the channel's page (ADR-0070).
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
          />
        ))}
      </ul>
    </div>
  )
}

function MemberLine({ name, member, channel }: { name: string; member?: LoopView; channel: string }) {
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
  // the channel's page, where that is done (ADR-0070), and muted for a loop with no surface, which is a
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
      <Link className="chan-warn" to={`/channels/${channel}`}>
        {hubOnly}no {words.kind} carries #{channel}
      </Link>
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
  telegram: { title: 'Telegram groups', kind: 'Telegram group' },
  slack: { title: 'Slack channels', kind: 'Slack channel' },
} as const

// A room as the surface names it: a Slack channel by its #name, or by its
// id until Slack has named it.
function roomName(surface: RoomSurface, room: Room): string {
  if (surface === 'slack') return room.title ? `#${room.title}` : room.room_id
  return room.title || 'untitled group'
}

type RoomSurface = keyof typeof ROOM_WORDS

// The loop's rooms, a section of its page's Surfaces card (#849): its
// Telegram groups or its Slack channels, read-only (ADR-0070). A channel's
// chat is added and bound on the channel's page, once for all its members,
// so this list only says where each room stands: the channel it carries,
// linking to that channel's page, or, unbound, where to bind it. The
// unbound ones come first, since they're what asks for the operator.
// Forget stays here, to clear a chat from the list. Absent on a hub from
// before rooms, which has no list to read.
export function LoopRooms({ loop }: { loop: LoopView }) {
  const rooms = useLoopRooms(loop)
  const channels = useQuery({ queryKey: ['channels'], queryFn: api.channels, retry: false })
  const surface = loopSurface(loop)
  if (!rooms || surface === '') return null
  const words = ROOM_WORDS[surface]
  const unbound = rooms.filter((room) => !room.channel)
  const bound = rooms.filter((room) => room.channel)
  const mine = (channels.data ?? []).filter((channel) => channel.loops.includes(loop.name)).map((c) => c.name)
  const bot = loop.tg_bot_username ? `@${loop.tg_bot_username}` : `@${loop.name}'s bot`
  return (
    <section className="surface-rooms" id={ROOMS_ANCHOR}>
      <h4 className="panel-sub">{words.title}</h4>
      <div className="loop-chans">
        {unbound.map((room) => (
          <UnboundRoom key={room.room_id} loop={loop} room={room} channels={mine} />
        ))}
        {rooms.length === 0 ? (
          <div className="chans-note">
            None yet. A channel's page adds{' '}
            {surface === 'slack'
              ? `@${loop.name}'s app to its Slack channel`
              : `${bot} to its Telegram group`}
            .
          </div>
        ) : (
          unbound.length === 0 && <div className="chans-note">Every {words.kind} carries a channel.</div>
        )}
        {bound.map((room) => (
          <BoundRoom key={room.room_id} loop={loop} room={room} />
        ))}
        {surface === 'slack' ? (
          <div className="chans-note">
            A Slack channel shows up here once someone allowed to reach the loops invites the app to it, or
            writes in it with the app invited. A stranger's invite or message records nothing: they link from
            Settings → Account first.
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

// Where an unbound room is bound, inside the room's own warning wash so
// the note reads as the room's: on the page of the channel it should
// carry, one of the loop's, reached from Channels (ADR-0070). Binding the
// chat there binds every member's room in it.
function BindWhere({ loop, channels }: { loop: string; channels: string[] }) {
  if (channels.length === 0)
    return (
      <div className="chans-note">
        Not read: @{loop} is in no channel for it to carry. Put it in one under{' '}
        <Link to="/channels">Channels</Link>.
      </div>
    )
  return (
    <div className="chans-note">
      Not read until it carries a channel. Bind it on that channel's page under{' '}
      <Link to="/channels">Channels</Link>:{' '}
      {channels.map((name, i) => (
        <span key={name}>
          {i > 0 && ', '}
          <Link to={`/channels/${name}`}>{channelLabel(name)}</Link>
        </span>
      ))}
      .
    </div>
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

// Forgetting a room, the one change the list still makes.
function useForget(loop: LoopView) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (room: Room) => api.forgetRoom(loop.name, room.surface, room.room_id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rooms', loop.name] }),
  })
}

function ErrorLine({ error }: { error: Error | null }) {
  if (!error) return null
  return (
    <div className="form-error" role="alert">
      {error.message}
    </div>
  )
}

function UnboundRoom({ loop, room, channels }: { loop: LoopView; room: Room; channels: string[] }) {
  const forget = useForget(loop)
  return (
    <div className="chan-room">
      <div className="chan-line">
        <span className="room-name unbound">{roomName(roomSurface(room), room)}</span>
        <ForgetButton loop={loop} room={room} busy={forget.isPending} onForget={() => forget.mutate(room)} />
      </div>
      <span className="room-id">{room.room_id}</span>
      <BindWhere loop={loop.name} channels={channels} />
      <ErrorLine error={forget.error} />
    </div>
  )
}

function BoundRoom({ loop, room }: { loop: LoopView; room: Room }) {
  const forget = useForget(loop)
  return (
    <div className="chan-room">
      <div className="chan-line">
        <span className="room-name">{roomName(roomSurface(room), room)}</span>
        <Link className="room-carries" to={`/channels/${room.channel}`}>
          {channelLabel(room.channel)}
        </Link>
        <ForgetButton loop={loop} room={room} busy={forget.isPending} onForget={() => forget.mutate(room)} />
      </div>
      <span className="room-id">{room.room_id}</span>
      <ErrorLine error={forget.error} />
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
  const confirmForget = () => {
    const what = !room.channel
      ? 'It is no longer listed.'
      : `@${loop.name} stops reading and posting there for #${room.channel}.`
    // A room the bot hears from again is recorded again, and bound at once
    // when it's the chat of a channel the loop is in (ADR-0070).
    const after =
      surface === 'slack'
        ? 'If an allowed person invites the app or writes there again, it comes back.'
        : 'If an allowed person adds the bot or writes there again, it comes back.'
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
