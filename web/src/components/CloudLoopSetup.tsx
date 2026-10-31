import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type CloudLoopSetup, type CloudLoopStep, type LoopView } from '../api'
import { environmentOptions, type Tone } from '../cloud'
import { checkSays, reposOf } from '../cloudLoop'
import { loginAccount } from '../claudeLogins'
import { formatAgo } from '../format'
import { CheckIcon, RefreshIcon } from './Icons'
import { Copyable } from './Copyable'
import { StepDialog } from './StepDialog'
import { Marked } from './Marked'
import { useMinuteClock } from './PlanUsage'

// A cloud loop's setup (ADR-0061, #1183), an admin's: the walk through
// what goes into the loop's own environment on claude.ai, in the order the
// operator does it there, and the hub's check that a session started there
// reaches it. The hub's setup view says which steps are left; each step
// here reads it, and a change answers with it or reads it again.

// The loop's setup view, polled while a check runs.
export function useCloudLoopSetup(loop: Pick<LoopView, 'name' | 'runtime'>, enabled: boolean) {
  return useQuery({
    queryKey: ['cloud-setup', loop.name],
    queryFn: () => api.cloudLoopSetup(loop.name),
    enabled: enabled && loop.runtime === 'cloud',
    refetchInterval: (q) => (q.state.data?.check?.state === 'running' ? 2000 : false),
  })
}

export function CloudLoopSetupDialog({ loop, onClose }: { loop: LoopView; onClose: () => void }) {
  const { data: setup, error: loadError } = useCloudLoopSetup(loop, true)
  const [busy, setBusy] = useState(false)
  // The token a mint answered, shown until the walk closes: the hub keeps
  // only its hash, so this is the one time it can be read.
  const [token, setToken] = useState('')
  const left = (step: CloudLoopStep) => setup?.steps.includes(step) ?? true

  return (
    <StepDialog
      title={`Set up @${loop.name}'s environment`}
      className="cloud-setup"
      busy={busy}
      onClose={onClose}
    >
      <div className="form">
        <p className="step-dialog-lede">
          A cloud loop runs in an environment of its own on claude.ai, starting from its GitHub repo. You make
          the environment by hand, since Spool can't, and the hub checks that a session started there reaches
          it. Until the check passes, the loop takes no turn and its messages wait.
        </p>
        {loadError ? (
          <div className="form-error" role="alert">
            Could not load the loop's setup:{' '}
            {loadError instanceof Error ? loadError.message : String(loadError)}
          </div>
        ) : !setup ? null : (
          <>
            {setup.settings && (
              <div className="cloud-warning">
                Settings → Cloud first: {setup.settings}.{' '}
                <Link className="text-link" to="/settings">
                  Open Settings → Cloud
                </Link>
              </div>
            )}
            <ol className="cloud-recipe cloud-loop-steps">
              <Step title="Its repo" done={!left('repo')}>
                <RepoStep loop={loop} setBusy={setBusy} />
              </Step>
              <Step title="Its environment on claude.ai" done={!left('environment') && !left('allowlist')}>
                <EnvironmentStep loop={loop} setup={setup} setBusy={setBusy} />
              </Step>
              <Step title="Its hub token" done={!left('token')}>
                <TokenStep loop={loop} setup={setup} token={token} setToken={setToken} setBusy={setBusy} />
              </Step>
              <Step title="Its check" done={!left('check')}>
                <CheckStep loop={loop} setup={setup} setBusy={setBusy} />
              </Step>
            </ol>
          </>
        )}
        <form method="dialog" className="pillar-actions">
          <button className={`btn${setup && setup.steps.length === 0 ? ' primary' : ''}`} disabled={busy}>
            {setup && setup.steps.length === 0 ? 'Done' : 'Close'}
          </button>
        </form>
      </div>
    </StepDialog>
  )
}

function Step({ title, done, children }: { title: string; done: boolean; children: ReactNode }) {
  return (
    <li className={done ? 'done' : ''}>
      <strong>{title}</strong>
      {done && (
        <span className="ok cloud-step-done">
          <CheckIcon size={12} /> done
        </span>
      )}
      {children}
    </li>
  )
}

// A change that answers with nothing the walk can use: the loop and its
// setup are read again.
function useLoopChange(loop: Pick<LoopView, 'name'>, setBusy: (busy: boolean) => void) {
  const qc = useQueryClient()
  const [error, setError] = useState('')
  const run = async (request: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await request()
      return true
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return false
    } finally {
      setBusy(false)
      void qc.invalidateQueries({ queryKey: ['cloud-setup', loop.name] })
      void qc.invalidateQueries({ queryKey: ['loop', loop.name] })
      void qc.invalidateQueries({ queryKey: ['loops'] })
    }
  }
  return { run, error }
}

function StepError({ error }: { error: string }) {
  return error ? (
    <div className="form-error" role="alert">
      <Marked text={error} />
    </div>
  ) : null
}

// The repo a session starts from, its branch, and the repos it attaches,
// which change only while the loop is stopped (ADR-0061).
function RepoStep({ loop, setBusy }: { loop: LoopView; setBusy: (busy: boolean) => void }) {
  const { run, error } = useLoopChange(loop, setBusy)
  const [repo, setRepo] = useState(loop.cloud_repo ?? '')
  const [branch, setBranch] = useState(loop.cloud_branch || 'main')
  const [more, setMore] = useState((loop.cloud_repos ?? []).join('\n'))
  const stopped = loop.status === 'paused'
  const changed =
    repo.trim() !== (loop.cloud_repo ?? '') ||
    branch.trim() !== (loop.cloud_branch || 'main') ||
    reposOf(more).join('\n') !== (loop.cloud_repos ?? []).join('\n')
  return (
    <>
      <p className="cloud-small">
        The GitHub repo each session starts from, and any it attaches. Install Claude's GitHub App on those
        repos only: the session reaches every repo the installation covers.
      </p>
      <CloudRepoFields
        idPrefix="cs"
        repo={repo}
        branch={branch}
        more={more}
        disabled={!stopped}
        onRepo={setRepo}
        onBranch={setBranch}
        onMore={setMore}
      />
      {stopped ? (
        <div className="cloud-code">
          <button
            className="btn"
            disabled={!changed || !repo.trim()}
            onClick={() =>
              void run(() =>
                api.patchLoop(loop.name, {
                  cloud_repo: repo.trim(),
                  cloud_branch: branch.trim(),
                  cloud_repos: reposOf(more),
                }),
              )
            }
          >
            Save
          </button>
        </div>
      ) : (
        <p className="cloud-small">Pause the loop to change these: a running session started from them.</p>
      )}
      <StepError error={error} />
    </>
  )
}

// The repo fields New loop and the walk share.
export function CloudRepoFields({
  idPrefix,
  repo,
  branch,
  more,
  disabled = false,
  onRepo,
  onBranch,
  onMore,
}: {
  idPrefix: string
  repo: string
  branch: string
  more: string
  disabled?: boolean
  onRepo: (repo: string) => void
  onBranch: (branch: string) => void
  onMore: (more: string) => void
}) {
  return (
    <>
      <div className="field-row">
        <div className="field">
          <label htmlFor={`${idPrefix}-repo`}>Repo</label>
          <input
            id={`${idPrefix}-repo`}
            className="mono"
            placeholder="owner/name"
            value={repo}
            disabled={disabled}
            spellCheck={false}
            onChange={(e) => onRepo(e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor={`${idPrefix}-branch`}>Branch</label>
          <input
            id={`${idPrefix}-branch`}
            className="mono"
            placeholder="main"
            value={branch}
            disabled={disabled}
            spellCheck={false}
            onChange={(e) => onBranch(e.target.value)}
          />
        </div>
      </div>
      <div className="field" style={{ marginTop: 10 }}>
        <label htmlFor={`${idPrefix}-repos`}>Further repos (optional)</label>
        <textarea
          id={`${idPrefix}-repos`}
          className="mono"
          rows={2}
          placeholder="owner/other, one a line"
          value={more}
          disabled={disabled}
          spellCheck={false}
          onChange={(e) => onMore(e.target.value)}
        />
      </div>
    </>
  )
}

// The environment, in OPERATIONS.md's order: make it on claude.ai, paste
// the allowed domains and the setup script, pick it here, and confirm the
// domains. The hub builds the domains on every read, and the confirm names
// their digest, so a list changed since is refused.
function EnvironmentStep({
  loop,
  setup,
  setBusy,
}: {
  loop: LoopView
  setup: CloudLoopSetup
  setBusy: (busy: boolean) => void
}) {
  const { run, error } = useLoopChange(loop, setBusy)
  const { allowlist } = setup
  const stopped = loop.status === 'paused'
  return (
    <>
      <ol className="cloud-substeps">
        <li>
          On{' '}
          <a href="https://claude.ai/code" target="_blank" rel="noreferrer">
            claude.ai/code
          </a>
          , signed in to {loginAccount(loop.claude_login)}, which the loop's sessions run under, open the
          environment selector under the message box and choose <strong>New environment</strong>. Name it{' '}
          <Copyable text={loop.name} />.
        </li>
        <li>
          Under <strong>Network access</strong>, choose <strong>Custom</strong> and paste this into{' '}
          <strong>Allowed domains</strong>. Leave{' '}
          <strong>Also include default list of common package managers</strong> unchecked.
          <Copyable text={allowlist.text} label="the allowed domains" />
          {allowlist.not_applied.length > 0 && (
            <div className="cloud-small">
              Left out, as an environment can't take a port or a bare <code>*</code>:{' '}
              {allowlist.not_applied.map((entry, i) => (
                <span key={entry}>
                  {i > 0 && ', '}
                  <code>{entry}</code>
                </span>
              ))}
              .
            </div>
          )}
        </li>
        <li>
          Paste this as its <strong>Setup script</strong>. It connects each session to this hub and holds no
          secret; it is the same for every cloud loop.
          {setup.script ? (
            <Copyable text={setup.script} label="the setup script" />
          ) : (
            <div className="cloud-small">It shows once Settings → Cloud has a public address.</div>
          )}
        </li>
        <li>
          Choose <strong>Add environment</strong>, then pick it here.
          {stopped ? (
            <EnvironmentPick
              login={loop.claude_login}
              chosen={loop.cloud_environment_id ?? ''}
              onPick={(id) => void run(() => api.patchLoop(loop.name, { cloud_environment_id: id }))}
            />
          ) : (
            <div className="cloud-small">
              {loop.cloud_environment_id ? (
                <>
                  <code>{loop.cloud_environment_id}</code>.{' '}
                </>
              ) : null}
              Pause the loop to change it.
            </div>
          )}
        </li>
        <li>
          Confirm you pasted the allowed domains. If Settings → Egress or the public address changes them, the
          hub asks you to paste them again.
          {allowlist.confirmed ? (
            <p className="step-dialog-state" aria-live="polite">
              <span className="ok">
                <CheckIcon size={14} /> The environment holds this hub's allowed domains.
              </span>
            </p>
          ) : (
            <div className="cloud-code">
              <button
                className="btn"
                onClick={() => void run(() => api.confirmCloudLoopAllowlist(loop.name, allowlist.digest))}
              >
                I pasted them
              </button>
            </div>
          )}
        </li>
      </ol>
      <StepError error={error} />
    </>
  )
}

// The environment, picked by name from the list of the account behind the
// loop's Claude login, which the hub reads on that login (ADR-0061,
// #1078). That list rests on an API Anthropic doesn't publish, so when it
// fails the step falls back to the documented way to an ID: /remote-env
// in the operator's own claude, signed in to that account.
export function EnvironmentPick({
  login,
  chosen,
  onPick,
}: {
  login: string
  chosen: string
  onPick: (id: string) => void
}) {
  const {
    data,
    error: listError,
    refetch,
    isFetching,
  } = useQuery({
    queryKey: ['cloud', 'environments', login || 'default'],
    queryFn: () => api.cloudEnvironments(login),
    refetchOnMount: 'always',
    retry: false,
  })
  const [pasted, setPasted] = useState('')

  if (listError) {
    const typed = pasted.trim()
    return (
      <>
        <div className="cloud-small">
          The hub couldn't list your environments (
          {listError instanceof Error ? listError.message : String(listError)}). Pick it in your own Claude
          Code instead, signed in to {loginAccount(login)}: run <Copyable text="/remote-env" /> in{' '}
          <code>claude</code>, choose the environment, then copy <code>remote.defaultEnvironmentId</code> from{' '}
          <code>~/.claude/settings.json</code> here.
        </div>
        <form
          className="egress-add cloud-code"
          onSubmit={(e) => {
            e.preventDefault()
            onPick(typed)
          }}
        >
          <input
            className="mono"
            value={pasted}
            onChange={(e) => setPasted(e.target.value)}
            placeholder={chosen || 'env_…'}
            aria-label="Environment ID"
            spellCheck={false}
          />
          <button className="btn" disabled={!typed || typed === chosen}>
            Save
          </button>
        </form>
      </>
    )
  }
  if (!data) return null
  const options = environmentOptions(data.environments, chosen)
  return (
    <div className="egress-add cloud-code">
      {data.environments.length === 0 && !chosen ? (
        <span className="cloud-small">No environments on {loginAccount(login)} yet.</span>
      ) : (
        <select value={chosen} onChange={(e) => onPick(e.target.value)} aria-label="Environment">
          {options.map((option) => (
            <option key={option.value} value={option.value} disabled={option.value === ''}>
              {option.label}
            </option>
          ))}
        </select>
      )}
      <button
        type="button"
        className="panel-action"
        onClick={() => void refetch()}
        disabled={isFetching}
        aria-label="Refresh the list"
        title="Refresh the list"
      >
        <RefreshIcon />
      </button>
    </div>
  )
}

// The loop's hub token (ADR-0061): minted here and shown once, set
// as the environment's network secret for the public host. A mint stops the
// loop and refuses the token it held at once, so a running loop asks first.
function TokenStep({
  loop,
  setup,
  token,
  setToken,
  setBusy,
}: {
  loop: LoopView
  setup: CloudLoopSetup
  token: string
  setToken: (token: string) => void
  setBusy: (busy: boolean) => void
}) {
  const { run, error } = useLoopChange(loop, setBusy)
  const host = setup.secret_host || 'the public address’s host'
  const mint = () => {
    if (
      setup.token.set &&
      !confirm(
        `Rotate @${loop.name}'s hub token? The loop stops, and the token it holds now is refused at once. ` +
          "Set the new one as the environment's network secret, then start the loop.",
      )
    )
      return
    void run(async () => setToken(await api.mintCloudToken(loop.name)))
  }
  return (
    <>
      <p className="cloud-small">
        The loop reaches this hub with a token of its own, which never enters the session: claude.ai adds it
        to each request for the hub's host. Minting one stops the loop until you start it again.
      </p>
      {token ? (
        <>
          <p className="cloud-small">
            In the environment's settings, add a <strong>network secret</strong> for <code>{host}</code> with
            this token. It is shown once: the hub keeps only its hash.
          </p>
          <Copyable text={token} label="the token" />
        </>
      ) : (
        <div className="cloud-code">
          {setup.token.set && (
            <span className="cloud-small">
              The loop holds a token. Rotate it if the environment's secret was lost or set wrong.{' '}
            </span>
          )}
          <button className={`btn${setup.token.set ? '' : ' primary'}`} onClick={mint}>
            {setup.token.set ? 'Rotate token' : 'Mint token'}
          </button>
        </div>
      )}
      <StepError error={error} />
    </>
  )
}

// The check (ADR-0061): one short session in the environment, which
// passes once it reaches the hub. It runs only on a started loop, as a
// stopped loop's token is refused.
function CheckStep({
  loop,
  setup,
  setBusy,
}: {
  loop: LoopView
  setup: CloudLoopSetup
  setBusy: (busy: boolean) => void
}) {
  const { run, error } = useLoopChange(loop, setBusy)
  const qc = useQueryClient()
  const now = useMinuteClock()
  const said = checkSays(setup, loop.cloud_repo ?? '', loop.claude_login)
  const running = setup.check?.state === 'running'
  const stopped = loop.status === 'paused'
  const check = () =>
    void run(async () => qc.setQueryData(['cloud-setup', loop.name], await api.checkCloudLoop(loop.name)))
  return (
    <>
      <p className="cloud-small">
        Starts one short session in the environment with a message that asks nothing of it. It passes once
        that session reaches this hub, and stays on claude.ai, where you can archive it.
      </p>
      <CheckState said={said} at={setup.check?.checked_at} now={now} />
      {said.hint && <div className="cloud-check-hint">{said.hint}</div>}
      {setup.check?.state === 'failed' && setup.check.detail && (
        <div className="cloud-check-detail">{setup.check.detail}</div>
      )}
      <div className="cloud-code">
        {stopped ? (
          <>
            <span className="cloud-small">
              Start the loop first: the hub refuses a stopped loop's token.{' '}
            </span>
            <button className="btn" onClick={() => void run(() => api.resume(loop.name))}>
              Start the loop
            </button>
          </>
        ) : (
          <button className="btn primary" onClick={check} disabled={running}>
            {running ? 'Checking…' : setup.check ? 'Check again' : 'Check'}
          </button>
        )}
      </div>
      <StepError error={error} />
    </>
  )
}

function CheckState({
  said,
  at,
  now,
}: {
  said: { text: string; tone: Tone }
  at: number | undefined
  now: number
}) {
  return (
    <p className={`cloud-loop-check ${said.tone}`} aria-live="polite">
      <span className="cloud-dot" aria-hidden />
      {said.text}
      {at ? ` · ${formatAgo(at, now)}` : ''}
    </p>
  )
}
