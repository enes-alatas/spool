import { useState } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type ChannelChat, type ChatMember, type LoopView } from '../api'
import { formatAgo } from '../format'
import {
  bindRefusal,
  chatName,
  chatSurface,
  CHAT_SURFACES,
  loopList,
  offSurface,
  statusText,
  surfaceWords,
  type ChatSurface,
} from '../surfaceChat'
import { loopSurface } from '../slack'
import { useMay } from './Session'
import { ExternalIcon } from './Icons'
import { POLL_MS } from './LinksSection'
import { StepDialog } from './StepDialog'

// A channel's Telegram group or Slack channel, on the channel's own page
// (ADR-0070, #1509). The chat is bound once for all the channel's members:
// the page adds it from one member's bot, lists each member's status under
// it, and unbinds it. rooms_changed refreshes it as bots arrive
// (stream.ts), so it updates without a reload.

// The detail's query key: the global stream refetches it on rooms_changed.
export const channelDetailKey = (channel: string) => ['channel-detail', channel]
const foundKey = (channel: string) => ['found-chats', channel]

export function SurfaceChat({ channel }: { channel: string }) {
  const detail = useQuery({ queryKey: channelDetailKey(channel), queryFn: () => api.channel(channel) })
  const { data: loops = [] } = useQuery({ queryKey: ['loops'], queryFn: api.loops })
  const [adding, setAdding] = useState<ChatSurface | null>(null)
  if (!detail.data) return null
  const { chat, surfaces, loops: members } = detail.data
  return (
    <section className="chan-chat" aria-label={`#${channel}'s chat`}>
      {chat ? (
        <BoundChat channel={channel} chat={chat} loops={loops} />
      ) : (
        <NoChat channel={channel} members={members} surfaces={surfaces} loops={loops} onAdd={setAdding} />
      )}
      {adding && (
        <AddChatDialog
          channel={channel}
          surface={adding}
          members={members}
          loops={loops}
          onClose={() => setAdding(null)}
        />
      )}
    </section>
  )
}

// No chat yet: an add per surface every member is on, and for a surface
// some aren't, which loops keep it from being offered.
function NoChat({
  channel,
  members,
  surfaces,
  loops,
  onAdd,
}: {
  channel: string
  members: string[]
  surfaces: string[]
  loops: LoopView[]
  onAdd: (surface: ChatSurface) => void
}) {
  const manage = useMay()('manage')
  const offered = CHAT_SURFACES.filter((surface) => surfaces.includes(surface))
  const withheld = CHAT_SURFACES.filter((surface) => !surfaces.includes(surface))
  return (
    <div className="chan-chat-none">
      <span>No Telegram group or Slack channel carries #{channel}.</span>
      {manage &&
        offered.map((surface) => (
          <button key={surface} className="btn sm" onClick={() => onAdd(surface)}>
            Add {surfaceWords(surface).name} {surfaceWords(surface).chat}
          </button>
        ))}
      {manage &&
        withheld.map((surface) => {
          const off = offSurface(members, loops, surface)
          if (off.length === 0) return null
          const words = surfaceWords(surface)
          return (
            <span key={surface} className="chan-chat-withheld">
              {words.name}: {loopList(off)} {off.length === 1 ? 'has' : 'have'} no {words.name} {words.bot}.
            </span>
          )
        })}
    </div>
  )
}

// The bound chat: its surface, name and id, Unbind, and each member's
// status under it.
function BoundChat({ channel, chat, loops }: { channel: string; chat: ChannelChat; loops: LoopView[] }) {
  const manage = useMay()('manage')
  const qc = useQueryClient()
  const words = surfaceWords(chat.surface)
  const unbind = useMutation({
    mutationFn: () => api.deleteChat(channel),
    onSettled: () => qc.invalidateQueries({ queryKey: channelDetailKey(channel) }),
  })
  const name = chatName(chat)
  const confirmUnbind = () => {
    const what = chat.seen_at === null ? `this ${words.chat}` : name
    if (
      confirm(
        `Unbind ${what} from #${channel}? Every loop in #${channel} stops carrying it there. Their bots stay in the ${words.chat}.`,
      )
    )
      unbind.mutate()
  }
  const bound = new Date(chat.bound_at).toLocaleDateString([], { month: 'short', day: 'numeric' })
  return (
    <>
      <div className="chan-chat-head">
        <span className="chan-chat-surface">
          {words.name} {words.chat}
        </span>
        <span className={chat.seen_at === null ? 'chan-chat-title unseen' : 'chan-chat-title'}>{name}</span>
        <span className="chan-chat-meta">
          {chat.chat_id} · bound {bound}
        </span>
        {manage && (
          <button className="btn sm" disabled={unbind.isPending} onClick={confirmUnbind}>
            Unbind
          </button>
        )}
      </div>
      {chat.seen_at === null && (
        <div className="chan-chat-warn">
          No member's {words.bot} is in this {words.chat} yet, so its title is unknown. Check the id: a
          mistyped one binds whichever {words.chat} turns out to have it.
        </div>
      )}
      {unbind.error && (
        <div className="form-error" role="alert">
          {unbind.error.message}
        </div>
      )}
      <ul className="chat-members" aria-label={`The loops in #${channel}`}>
        {chat.members.map((member) => (
          <MemberRow key={member.loop} channel={channel} chat={chat} member={member} loops={loops} />
        ))}
      </ul>
    </>
  )
}

// One member loop under the chat: its status, and what gets it carrying.
function MemberRow({
  channel,
  chat,
  member,
  loops,
}: {
  channel: string
  chat: ChannelChat
  member: ChatMember
  loops: LoopView[]
}) {
  const manage = useMay()('manage')
  const qc = useQueryClient()
  const words = surfaceWords(chat.surface)
  const bind = useMutation({
    mutationFn: () => api.bindChatMember(channel, member.loop),
    onSettled: () => qc.invalidateQueries({ queryKey: channelDetailKey(channel) }),
  })
  const loop = loops.find((l) => l.name === member.loop)
  const known = ['carrying', 'not_in_chat', 'no_bot', 'unbound'].includes(member.status)
  return (
    <li className="chat-member">
      <span className="chat-member-loop">@{member.loop}</span>
      <span className={`chat-member-status ${known ? member.status : 'not_in_chat'}`}>
        {statusText(member.status, chat.surface)}
      </span>
      <span className="chat-member-act">
        {bind.error && (
          <span className="form-error" role="alert">
            {bind.error.message}
          </span>
        )}
        {manage && member.status === 'not_in_chat' && member.add_link && (
          <a className="btn sm" href={member.add_link} target="_blank" rel="noreferrer">
            Add {loop?.tg_bot_username ? `@${loop.tg_bot_username}` : `@${member.loop}'s bot`}{' '}
            <ExternalIcon size={12} />
          </a>
        )}
        {member.status === 'not_in_chat' && chatSurface(chat.surface) === 'slack' && (
          <span className="chat-member-hint">invite its app in Slack</span>
        )}
        {member.status === 'no_bot' && (
          <Link className="panel-link" to={`/loops/${member.loop}`}>
            Add a {words.name} {words.bot} on its page
          </Link>
        )}
        {manage && member.status === 'unbound' && (
          <button className="btn sm" disabled={bind.isPending} onClick={() => bind.mutate()}>
            Bind
          </button>
        )}
      </span>
    </li>
  )
}

// The Telegram link that adds a loop's bot to a group of the operator's
// choosing, as the hub's own add_link is made; "" for a bot whose name the
// hub doesn't know.
function startGroupLink(loop: LoopView | undefined): string {
  return loop?.tg_bot_username
    ? `https://t.me/${encodeURIComponent(loop.tg_bot_username)}?startgroup=spool`
    : ''
}

// Step one's words: pick the loop whose bot goes in first, or, for a
// channel of one loop, that loop's.
function firstStep(surface: ChatSurface, only: string): string {
  if (surface === 'slack')
    return only
      ? `Invite @${only}'s app to the channel in Slack.`
      : 'Pick the loop whose app goes in first, and invite it to the channel in Slack.'
  return only
    ? `Add @${only}'s bot to the group in Telegram.`
    : 'Pick the loop whose bot goes in first, and add it to the group in Telegram.'
}

// Adding the channel's chat: one member's bot goes in first, then the
// operator confirms the chat it found, or pastes the chat's id (ADR-0070).
// The found list waits on the chat live.
function AddChatDialog({
  channel,
  surface,
  members,
  loops,
  onClose,
}: {
  channel: string
  surface: ChatSurface
  members: string[]
  loops: LoopView[]
  onClose: () => void
}) {
  const qc = useQueryClient()
  const words = surfaceWords(surface)
  const on = loops.filter((loop) => members.includes(loop.name) && loopSurface(loop) === surface)
  // The operator's pick, '' until they make one. The default is derived,
  // not stored: the loops may load after the dialog opens.
  const [picked, setPicked] = useState('')
  const first = picked || on[0]?.name || ''
  const [pasted, setPasted] = useState('')
  // rooms_changed refreshes the list (stream.ts); a poll covers a stream
  // that dropped while the operator was in Telegram or Slack.
  const found = useQuery({
    queryKey: foundKey(channel),
    queryFn: () => api.foundChats(channel),
    refetchInterval: POLL_MS,
  })
  const chats = (found.data ?? []).filter((chat) => chat.surface === surface)
  const put = useMutation({
    mutationFn: (chatID: string) => api.putChat(channel, { surface, chat_id: chatID }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: channelDetailKey(channel) })
      onClose()
    },
  })
  const firstLoop = loops.find((loop) => loop.name === first)
  const link = surface === 'telegram' ? startGroupLink(firstLoop) : ''
  // Step one stays open while chats are listed: one found earlier may not
  // be the one the operator is adding now.
  const listed = chats.length > 0
  return (
    <StepDialog
      title={`Add a ${words.name} ${words.chat} to #${channel}`}
      className="chat-dialog"
      onClose={onClose}
      busy={put.isPending}
    >
      <div className="form">
        <ol className="link-steps">
          <li className="now">
            <span className="link-step-mark" aria-hidden>
              1
            </span>
            <span>
              {firstStep(surface, on.length > 1 ? '' : first)}
              {(on.length > 1 || link) && (
                <span className="chat-dialog-pick">
                  {on.length > 1 && (
                    <select
                      className="panel-select"
                      aria-label={`The loop whose ${words.bot} goes in first`}
                      value={first}
                      onChange={(e) => setPicked(e.target.value)}
                    >
                      {on.map((loop) => (
                        <option key={loop.name} value={loop.name}>
                          @{loop.name}
                        </option>
                      ))}
                    </select>
                  )}
                  {link && (
                    <a className="btn sm" href={link} target="_blank" rel="noreferrer">
                      Add @{firstLoop?.tg_bot_username} to a group <ExternalIcon size={12} />
                    </a>
                  )}
                </span>
              )}
            </span>
          </li>
          <li className={listed ? 'now' : ''}>
            <span className="link-step-mark" aria-hidden>
              2
            </span>
            <span>
              Confirm the {words.chat} here. The other members' {words.bot}s go in after.
            </span>
          </li>
        </ol>
        {listed ? (
          <ul className="chat-found" aria-label={`${words.name} ${words.chat}s found`}>
            {chats.map((chat) => (
              <li key={chat.chat_id}>
                <span className="chat-found-name">
                  {chat.title || chat.chat_id}
                  <span className="chat-found-by">
                    found by {loopList(chat.found_by)} · {formatAgo(chat.first_seen_at, found.dataUpdatedAt)}
                  </span>
                </span>
                <button className="btn sm" disabled={put.isPending} onClick={() => put.mutate(chat.chat_id)}>
                  Use this {words.chat}
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="step-dialog-state group-wait" aria-live="polite">
            <span className="group-wait-dot" aria-hidden />
            Waiting for a {words.chat}…
            <span className="hint">
              {surface === 'telegram'
                ? 'Nothing yet? Only an add by an account linked to Spool counts: link yours under Settings → Account.'
                : "Nothing yet? Only an invite by an account linked to Spool counts. For an app made before v0.9.0, the hub waits for a linked account's first message there, until the app subscribes to member_joined_channel."}
            </span>
          </p>
        )}
        <form
          className="chat-paste"
          onSubmit={(e) => {
            e.preventDefault()
            if (pasted.trim()) put.mutate(pasted.trim())
          }}
        >
          <label htmlFor="chat-paste-id">Or paste the {words.chat}'s id</label>
          <div className="chat-paste-row">
            <input
              id="chat-paste-id"
              value={pasted}
              placeholder={surface === 'telegram' ? '-100…' : 'C…'}
              onChange={(e) => setPasted(e.target.value)}
              autoComplete="off"
              spellCheck={false}
            />
            <button className="btn sm" disabled={!pasted.trim() || put.isPending}>
              Bind
            </button>
          </div>
        </form>
        {put.error && (
          <div className="form-error" role="alert">
            {bindRefusal(put.error, channel, members, loops, surface)}
          </div>
        )}
        <form method="dialog" className="pillar-actions">
          <button className="btn">Close</button>
        </form>
      </div>
    </StepDialog>
  )
}
