import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type LoopView, type Me } from '../api'
import { linkName, linkSnapshot, type OpenLinkCode } from '../links'
import { ownerChoices, selfOwner } from '../owner'
import { loopSurface } from '../slack'
import { CheckIcon } from './Icons'
import { LinkSteps, POLL_MS, Waiting, linkSurface } from './LinksSection'
import { StepDialog } from './StepDialog'
import { BotTokenForm, SlackStep } from './SurfaceAttach'
import { Copyable } from './Copyable'

// The Chat surface card's dialog (#589): the whole step without leaving the
// first-run page. A loop with no surface gets one through the loop page's
// own attach forms; one with a surface has the operator link their own
// account through it (#775), as Settings → Account does. The step is done once a
// surface has carried a message each way, which the onboarding read says.
export function SurfaceDialog({ loops, onClose }: { loops: LoopView[]; onClose: () => void }) {
  const [picked, setPicked] = useState(loops[0]?.name ?? '')
  // The loop as the fleet list has it now: attaching refreshes that list,
  // and its surface is what moves the dialog on to linking.
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
          Create a bot with <strong>@BotFather</strong> on Telegram (<Copyable text="/newbot" />
          ), then paste the token it gives you.
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

// A loop with a surface: link your own account through its bot or app, and
// message it (#775). Pairing codes are gone (ADR-0051), so a stranger's DM
// is answered with nothing: a link is how the operator reaches the loop.
// The code and its steps sit inline, the Account page's own, and the link
// query is polled while the dialog is open.
function Pair({ loop }: { loop: LoopView }) {
  const slack = loopSurface(loop) === 'slack'
  const surface = linkSurface(slack ? 'slack' : 'telegram')
  const to = slack ? `the ${loop.name} app` : `@${loop.tg_bot_username || loop.name}`
  const [code, setCode] = useState<OpenLinkCode | null>(null)
  const [error, setError] = useState('')
  const { data: me } = useQuery({ queryKey: ['me'], queryFn: api.me })
  // An owner-role link owns a Telegram loop with no owner on its own
  // (ADR-0051); on Slack this dialog does the same once they are linked.
  const unowned = slack ? !loop.owner_slack_user_id : loop.owner_tg_user_id === undefined
  const owns = !!me?.name && me.role === 'owner' && unowned
  // A token session is no user, so the hub refuses it a code (no_user).
  const tokenSession = me !== undefined && me.via !== 'password'
  const { data } = useQuery({
    queryKey: ['me', 'links'],
    queryFn: () => linkSnapshot(api.myWaitingLinks, api.myLinks),
    refetchInterval: POLL_MS,
    enabled: !tokenSession,
  })
  const link = data?.links.find((l) => l.surface === surface.surface)
  const wait = data?.waiting.find((l) => l.surface === surface.surface)
  const askCode = useMutation({
    mutationFn: () => api.linkCode(surface.surface),
    onSuccess: (minted) => {
      setCode({ ...minted, surface: surface.surface })
      setError('')
    },
    onError: (e) => setError(e instanceof Error ? e.message : String(e)),
  })

  return (
    <>
      <p className="step-dialog-lede">
        Link your own {surface.label} account, so {to} knows it is you: get a code, send it to {to} in a
        direct message, and reply YES when it asks.{' '}
        {owns
          ? 'Linking makes you its owner, the one person it can message before being messaged. Then message it again.'
          : slack && unowned
            ? 'Then make yourself its owner, the one person it can message before being messaged, and message it again.'
            : 'Then message it again.'}{' '}
        The step is done when the loop answers.
      </p>
      {code ? (
        <LinkSteps
          key={code.code}
          code={code}
          surface={surface}
          link={link}
          wait={wait}
          to={to}
          onAgain={() => askCode.mutate()}
        />
      ) : link ? (
        <p className="step-dialog-state">
          <span className="ok">
            <CheckIcon size={14} /> Linked: {linkName(link)}.
          </span>
        </p>
      ) : wait ? (
        <Waiting link={wait} app={surface.app} />
      ) : tokenSession ? (
        <div className="hint">
          A link belongs to a user, and this session signed in with the operator token. Sign out and sign in
          as your user, the first start's admin, to link your account.
        </div>
      ) : (
        <div className="pillar-actions">
          <button className="btn primary" onClick={() => askCode.mutate()} disabled={askCode.isPending}>
            Get a link code
          </button>
        </div>
      )}
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      {link && me?.name && loop.owner_user === me.name ? (
        <p className="step-dialog-state">
          <span className="ok">
            <CheckIcon size={14} /> You are its owner.
          </span>
        </p>
      ) : (
        slack && link && !loop.owner_slack_user_id && <SlackOwner loop={loop} me={me} />
      )}
    </>
  )
}

// A Slack loop with no owner, once the operator is linked. Unlike Telegram,
// a link makes nobody owner of a Slack loop, and only the owner is someone
// the loop can message before they message it (ADR-0049). A user whose role
// is owner is made its owner here at once, as Telegram does for them (Enes,
// 2026-10-13 on #792). Anyone else is offered the users linked on Slack in
// the loop's workspace, as the loop page's owner picker offers them (#732),
// and so is an owner whose own attempt was refused.
function SlackOwner({ loop, me }: { loop: LoopView; me?: Me }) {
  const qc = useQueryClient()
  const { data: senders } = useQuery({
    queryKey: ['slack-senders'],
    queryFn: api.slackSenders,
    refetchInterval: POLL_MS,
  })
  const { data: status } = useQuery({
    queryKey: ['slack-status', loop.name],
    queryFn: () => api.slackStatus(loop.name),
  })
  const teamID = status?.team_id
  const { choices } = ownerChoices(
    (senders ?? [])
      .filter((s) => !teamID || s.team_id === teamID)
      .map((s) => ({ user: s.user, status: s.status, handle: s.username ? `@${s.username}` : s.display })),
    { set: false, user: '', handle: '' },
  )
  const offered = choices.filter((c) => c.value)
  const setOwner = useMutation({
    mutationFn: (user: string) => api.setOwner(loop.name, user),
    onSuccess: (updated) => {
      qc.setQueryData(['loop', loop.name], updated)
      return qc.invalidateQueries({ queryKey: ['loops'] })
    },
  })
  const auto = selfOwner(me, offered)
  const tried = useRef(false)
  useEffect(() => {
    if (!auto || tried.current) return
    tried.current = true
    setOwner.mutate(auto)
  }, [auto, setOwner])
  if (offered.length === 0) return null
  if (auto && !setOwner.error) return <div className="hint">Making you its owner…</div>
  return (
    <div className="field">
      <label>Owner</label>
      {offered.map((c) => (
        <div key={c.value} className="feed-item">
          <span>{c.label}</span>
          <button
            className="btn sm primary"
            disabled={setOwner.isPending}
            onClick={() => setOwner.mutate(c.value)}
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
