import { Navigate, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import { ChannelChat } from '../components/ChannelChat'
import { SurfaceChat } from '../components/SurfaceChat'
import { channelLabel } from '../channels'

// A channel's chat as a page of its own, opened from its card on Channels
// (#1443). Every channel has one, `fleet` as any other (ADR-0068). Only a
// list that loaded without the name lands back on Channels: a failed load
// says so, rather than bouncing the operator off a chat they had open.
// Above the conversation sits the channel's Telegram group or Slack channel,
// added and bound here once for all its members (ADR-0070, #1509).
export default function ChannelChatPage() {
  const { name = '' } = useParams()
  const channels = useQuery({ queryKey: ['channels'], queryFn: api.channels })
  if (channels.isPending) return <div className="page measure placeholder">Loading…</div>
  if (!channels.data)
    return (
      <div className="page measure">
        <h1>{channelLabel(name)}</h1>
        <div className="form-error" role="alert">
          Could not load {channelLabel(name)}:{' '}
          {channels.error instanceof Error ? channels.error.message : String(channels.error)}
        </div>
      </div>
    )
  const channel = channels.data.find((c) => c.name === name)
  if (!channel) return <Navigate to="/channels" replace />
  return (
    <div className="page">
      <h1>{channelLabel(channel.name)}</h1>
      <SurfaceChat channel={channel.name} />
      <ChannelChat channel={channel.name} members={channel.loops} />
    </div>
  )
}
