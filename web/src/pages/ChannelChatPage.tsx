import { Navigate, useParams } from 'react-router'
import { ChannelChat } from '../components/ChannelChat'
import { FLEET_CHANNEL } from '../channels'

// A channel's chat as a page of its own, opened from its card on Channels
// (#1443). Until now it was a tab on the Fleet page (#286), which now shows
// the fleet and nothing else. Only the fleet channel's chat opens so far; any
// other name lands back on Channels.
export default function ChannelChatPage() {
  const { name = '' } = useParams()
  if (name !== FLEET_CHANNEL) return <Navigate to="/channels" replace />
  return (
    <div className="page">
      <h1>Fleet channel</h1>
      <ChannelChat channel={name} />
    </div>
  )
}
