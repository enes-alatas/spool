import { Link } from 'react-router'
import type { LoopView } from '../api'
import { cloudHold, cloudSessionCondition, useCloudSettings } from '../cloudLoop'
import { MCPReach } from './MCPReach'

// The Workstation card of a cloud loop (ADR-0056, #867): its session runs on
// Anthropic's machine, so there is nothing here to power. The card says
// where the session is and, while the hub holds the loop, where that is
// fixed.
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
        Anthropic's machine: there is nothing here to power. It reaches what its environment's network access
        on claude.ai allows, not this hub's egress list.
      </div>
    </div>
  )
}
