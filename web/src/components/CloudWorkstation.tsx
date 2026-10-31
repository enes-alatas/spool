import { Link, useSearchParams } from 'react-router'
import type { LoopView } from '../api'
import {
  checkSays,
  cloudHold,
  cloudSessionCondition,
  holdIsLoops,
  stepsLeft,
  useCloudSettings,
} from '../cloudLoop'
import { CloudLoopSetupDialog, useCloudLoopSetup } from './CloudLoopSetup'
import { LoopLogin } from './LoopLogin'
import { MCPReach } from './MCPReach'
import { useMay } from './Session'

// The search parameter that opens the loop's setup walk, so New loop and a
// note elsewhere on the page can send the operator straight to it.
export const SETUP_PARAM = 'setup'

// The Workstation card of a cloud loop (ADR-0056, #867): its session runs on
// Anthropic's machine, so there is nothing here to power. The card says
// where the session is, the repo and environment it starts from
// (ADR-0061), and, while the hub holds the loop, where that is fixed: the
// loop's own setup walk for its own steps, Settings → Cloud for the hub's.
export function CloudWorkstation({ loop }: { loop: LoopView }) {
  const hold = cloudHold(loop, useCloudSettings(loop))
  const condition = cloudSessionCondition(loop, hold)
  const manage = useMay()('manage')
  const { data: setup } = useCloudLoopSetup(loop, manage)
  const [params, setParams] = useSearchParams()
  const walking = manage && params.has(SETUP_PARAM)
  const walk = (open: boolean) =>
    setParams(
      (p) => {
        if (open) p.set(SETUP_PARAM, '')
        else p.delete(SETUP_PARAM)
        return p
      },
      { replace: true },
    )
  const repo = loop.cloud_repo ? `${loop.cloud_repo}@${loop.cloud_branch || 'main'}` : ''
  const more = loop.cloud_repos?.length ?? 0
  const check = setup && checkSays(setup, loop.cloud_repo ?? '', loop.claude_login)
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
      <LoopLogin loop={loop} />
      <div className="row">
        <span className="k">repo</span>
        <span className="v mono">
          {repo || 'none yet'}
          {more > 0 && ` +${more} attached`}
        </span>
      </div>
      <div className="row">
        <span className="k">environment</span>
        <span className="v mono">{loop.cloud_environment_id || 'none yet'}</span>
      </div>
      {check && (
        <div className="row">
          <span className="k">check</span>
          <span className={`v ${check.tone}`}>{check.text}</span>
        </div>
      )}
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
          {holdIsLoops(hold) ? (
            <>
              Needs setup, so no turn starts and messages wait.
              {setup && setup.steps.length > 0 && <> Still to do: {stepsLeft(setup.steps)}.</>}
              {!manage && ' An admin sets it up on this page.'}
            </>
          ) : (
            <>
              Needs setup: {hold}, so no turn starts and messages wait. Fix it in{' '}
              <Link className="text-link" to="/settings">
                Settings → Cloud
              </Link>
              .
            </>
          )}
        </div>
      )}
      {manage && (
        <div className="controls" style={{ marginTop: 12 }}>
          <button className={`btn sm${holdIsLoops(hold) ? ' primary' : ''}`} onClick={() => walk(true)}>
            {setup && setup.steps.length === 0 ? 'Setup' : 'Set up'}
          </button>
        </div>
      )}
      <div className="panel-note">
        Anthropic's machine: there is nothing here to power. It reaches what its environment's network access
        on claude.ai allows: this hub's allowlist once you paste it there.
      </div>
      {walking && <CloudLoopSetupDialog loop={loop} onClose={() => walk(false)} />}
    </div>
  )
}
