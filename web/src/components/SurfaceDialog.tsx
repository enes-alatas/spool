import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type LoopView, type SlackSender } from '../api'
import { loopSurface, slackSenderLabel } from '../slack'
import { CheckIcon } from './Icons'
import { SenderRow, slackEntry, telegramEntry } from './SenderRow'
import { StepDialog } from './StepDialog'
import { BotTokenForm, SlackStep } from './SurfaceAttach'

// The Chat surface card's dialog (#589): the whole step without leaving the
// first-run page. A loop with no surface gets one through the loop page's
// own attach forms; one with a surface asks the operator to message it, and
// lists whoever is waiting for approval with the pairing-code field Access
// has, so the operator allows themselves here. The step is done once a
// surface has carried a message each way, which the onboarding read says.
export function SurfaceDialog({ loops, onClose }: { loops: LoopView[]; onClose: () => void }) {
  const [picked, setPicked] = useState(loops[0]?.name ?? '')
  // The loop as the fleet list has it now: attaching refreshes that list,
  // and its surface is what moves the dialog on to pairing.
  const { data: fleet } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const loop = (fleet ?? loops).find((l) => l.name === picked)
  const { data: onboarding } = useQuery({ queryKey: ['onboarding'], queryFn: api.onboarding, retry: false })
  const done = onboarding?.surface.done === true

  return (
    <StepDialog title="Chat surface" className="surface-dialog" onClose={onClose}>
      {done ? (
        <div className="form">
          <p className="step-dialog-state" aria-live="polite">
            <span className="ok">
              <CheckIcon size={14} /> A bot has carried messages both ways.
            </span>
          </p>
          <form method="dialog" className="pillar-actions">
            <button className="btn primary">Done</button>
          </form>
        </div>
      ) : (
        <div className="form">
          {loops.length > 1 && (
            <div className="field">
              <label htmlFor="surface-dialog-loop">Loop</label>
              <select id="surface-dialog-loop" value={picked} onChange={(e) => setPicked(e.target.value)}>
                {loops.map((l) => (
                  <option key={l.name} value={l.name}>
                    @{l.name}
                  </option>
                ))}
              </select>
            </div>
          )}
          {loop && (loopSurface(loop) === '' ? <Attach key={loop.name} loop={loop} /> : <Pair loop={loop} />)}
          {onboarding?.surface.reason && <div className="pillar-state">{onboarding.surface.reason}</div>}
        </div>
      )}
    </StepDialog>
  )
}

// A loop with no surface: the choice the loop page offers, then that
// surface's form. Saving closes the form, and the loop's new surface moves
// the dialog on.
function Attach({ loop }: { loop: LoopView }) {
  const [surface, setSurface] = useState<'' | 'telegram' | 'slack'>('')
  if (surface === 'telegram')
    return (
      <>
        <p className="step-dialog-lede">
          Create a bot with <strong>@BotFather</strong> on Telegram (<code>/newbot</code>), then paste the
          token it gives you.
        </p>
        <BotTokenForm loop={loop} startOpen onClose={() => setSurface('')} />
      </>
    )
  if (surface === 'slack') return <SlackStep loop={loop} onClose={() => setSurface('')} />
  return (
    <>
      <p className="step-dialog-lede">@{loop.name} has no bot yet. Give it one on the surface you chat on.</p>
      <div className="pillar-actions">
        <button className="btn primary" onClick={() => setSurface('telegram')}>
          Attach Telegram
        </button>
        <button className="btn" onClick={() => setSurface('slack')}>
          Attach Slack
        </button>
      </div>
    </>
  )
}

// A loop with a surface: message it, and allow yourself with the code it
// answers with. Whoever is waiting on that surface is listed, polled while
// the dialog is open, since a first message lands at any moment.
function Pair({ loop }: { loop: LoopView }) {
  const qc = useQueryClient()
  const slack = loopSurface(loop) === 'slack'
  // The same caches Access reads, raw, so either page's approval shows in
  // the other.
  const telegram = useQuery({
    queryKey: ['senders'],
    queryFn: api.senders,
    enabled: !slack,
    refetchInterval: 3000,
  })
  const slackSenders = useQuery({
    queryKey: ['slack-senders'],
    queryFn: api.slackSenders,
    enabled: slack,
    refetchInterval: 3000,
  })
  const entries = slack ? (slackSenders.data ?? []).map(slackEntry) : (telegram.data ?? []).map(telegramEntry)
  const waiting = entries.filter((s) => s.status === 'pending')
  const refresh = () => qc.invalidateQueries({ queryKey: [slack ? 'slack-senders' : 'senders'] })

  return (
    <>
      <p className="step-dialog-lede">
        {slack ? (
          <>
            Send the {loop.name} app a direct message in Slack from your own account. It answers with a
            pairing code: type it below to allow yourself, then make yourself its owner, since the app takes
            direct messages only from its owner. Then message it again; the step is done when the loop
            answers.
          </>
        ) : (
          <>
            Message <strong>@{loop.tg_bot_username || loop.name}</strong> on Telegram from your own account.
            It answers with a pairing code: type it below to allow yourself. Once you are allowed, message it
            again; the step is done when the loop answers.
          </>
        )}
      </p>
      {waiting.length > 0 ? (
        waiting.map((s) => <SenderRow key={s.key} s={s} refresh={refresh} />)
      ) : (
        <div className="hint">Waiting for a message…</div>
      )}
      {slack && !loop.owner_slack_user_id && <SlackOwner loop={loop} senders={slackSenders.data ?? []} />}
    </>
  )
}

// A Slack loop with no owner. Unlike Telegram, allowing a sender makes
// nobody owner, and the app's DM reaches the loop only from its owner, so
// the allowed senders are offered as owner here. Only one in the loop's
// workspace can be owner, so only they are offered, as the loop page's
// owner picker does.
function SlackOwner({ loop, senders }: { loop: LoopView; senders: SlackSender[] }) {
  const qc = useQueryClient()
  const { data: status } = useQuery({
    queryKey: ['slack-status', loop.name],
    queryFn: () => api.slackStatus(loop.name),
  })
  const teamID = status?.team_id
  const allowed = senders.filter((s) => s.status === 'allowed' && (!teamID || s.team_id === teamID))
  const setOwner = useMutation({
    mutationFn: (id: string) => api.setSlackOwner(loop.name, id),
    onSuccess: (updated) => {
      qc.setQueryData(['loop', loop.name], updated)
      return qc.invalidateQueries({ queryKey: ['loops'] })
    },
  })
  if (allowed.length === 0) return null
  return (
    <div className="field">
      <label>Owner</label>
      {allowed.map((s) => (
        <div key={s.slack_user_id} className="feed-item">
          <span>{slackSenderLabel(s)}</span>
          <button
            className="btn sm primary"
            disabled={setOwner.isPending}
            onClick={() => setOwner.mutate(s.slack_user_id)}
          >
            Make owner
          </button>
        </div>
      ))}
      {setOwner.error && (
        <div className="form-error" role="alert">
          {setOwner.error instanceof Error ? setOwner.error.message : String(setOwner.error)}
        </div>
      )}
    </div>
  )
}
