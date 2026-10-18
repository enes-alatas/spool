import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { api, type LoopView } from '../api'
import { cloudHold, cloudSessionCondition, useCloudSettings } from '../cloudLoop'
import { allowlistSyncText } from '../egress'
import { MCPReach } from './MCPReach'
import { useMinuteClock } from './PlanUsage'

// The Workstation card of a cloud loop (ADR-0056, #867): its session runs on
// Anthropic's machine, so there is nothing here to power. The card says
// where the session is, the repo it starts from, whether its environment
// carries the allowlist as it stands, and, while the hub holds the loop,
// where that is fixed.
export function CloudWorkstation({ loop }: { loop: LoopView }) {
  const hold = cloudHold(loop, useCloudSettings(loop))
  const condition = cloudSessionCondition(loop, hold)
  return (
    <div className="side-panel">
      <h3>Workstation</h3>
      <div className="row" style={{ alignItems: 'center' }}>
        <span className="k">status</span>
        <span className="v" style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span className={`state-dot ${condition.alert ? 'state-workstation_down' : 'state-idle'}`} />
          {condition.status}
        </span>
      </div>
      <div className="row">
        <span className="k">runtime</span>
        <span className="v">cloud</span>
      </div>
      <div className="row">
        <span className="k">session</span>
        <span className="v">
          {loop.cloud_session_url ? (
            <a className="text-link" href={loop.cloud_session_url} target="_blank" rel="noopener noreferrer">
              open on claude.ai ↗
            </a>
          ) : (
            'none yet'
          )}
        </span>
      </div>
      <div className="row">
        <span className="k">repo</span>
        <span className="v">
          {loop.cloud_repo ? loop.cloud_repo.replace('https://github.com/', '') : 'none'}
        </span>
      </div>
      <AllowlistRow loop={loop} />
      <MCPReach loop={loop} />
      {hold && (
        <div className="panel-note bad">
          Held: {hold}, so no turn starts and messages wait. Fix it in{' '}
          <Link className="text-link" to="/settings">
            Settings → Cloud
          </Link>
          .
        </div>
      )}
      <div className="panel-note">
        Anthropic's machine: there is nothing here to power. Its allowlist is this hub's, enforced by
        Anthropic's proxy, not this hub's egress wall.
      </div>
    </div>
  )
}

// Whether the loop's cloud environment carries the allowlist as it stands
// (Settings → Egress). No row before the hub reports the environment, which
// its first wake makes.
function AllowlistRow({ loop }: { loop: LoopView }) {
  const { data } = useQuery({ queryKey: ['egress'], queryFn: api.egress })
  const now = useMinuteClock()
  const environment = data?.cloud?.environments.find((e) => e.loop === loop.name)
  if (!environment) return null
  return (
    <div className="row">
      <span className="k">allowlist</span>
      <span className={`v ${environment.in_sync ? '' : 'bad'}`}>{allowlistSyncText(environment, now)}</span>
    </div>
  )
}
