// Typed client for the Spool REST API.

export interface LoopView {
  id: string
  name: string
  mission: string
  model: string
  workspace_mode: 'none' | 'dir' | 'worktree'
  workspace_path: string
  repo_path: string
  worktree_path: string
  branch: string
  runtime: 'bare' | 'docker'
  image: string
  mem_mb: number
  cpus: number
  tick_interval_sec: number
  min_wake_sec: number
  max_wake_sec: number
  idle_timeout_sec: number
  pacing: 'fixed' | 'self'
  effort: string
  tg_bot_username: string
  tg_group_chat_id: number
  status: 'active' | 'paused' | 'archived'
  current_session_id: string
  current_pid: number
  created_at: number
  updated_at: number
  state: string
  next_tick_at: number
  cost_today_usd: number
  // The calendar day cost_today_usd sums, YYYY-MM-DD in the server's zone.
  // The boundary is the operator's midnight, not UTC's, so the client shows
  // the day rather than deriving one.
  cost_day: string
  // Context occupancy: what the latest turn carried into the model, against
  // that model's window. The limit is 0 when the model is unknown to Spool,
  // in which case there is no honest percentage to show.
  context_tokens: number
  context_limit_tokens: number
  // Occupancy as a percentage of the window, computed by the server so the
  // gauge and rotation judge the same number (0 = unmeasured).
  context_fill_pct: number
  // How many messages this loop sent never reached their surface and that
  // nobody has dealt with: no successful retry, no dismissal (#202, #269). No age
  // limit — a failure stops counting when someone resolves it, not when a
  // clock decides the operator is done looking. The server counts it,
  // because the activity feed the client can see is a newest-N window and
  // would undercount silently on a busy fleet — and a count quietly wrong
  // about a delivery failure reads as a checked zero.
  undelivered: number
  has_tg_token: boolean
  workstation_up: boolean
  workstation_detail?: string
  // Why the workstation is down: '' while it is up, 'powered_off' when the
  // operator switched it off, 'unreachable' when it died on its own.
  down_reason: '' | 'powered_off' | 'unreachable'
  // The allowlisted Telegram sender this loop may message privately, and the
  // handle to show for them. Absent when no owner is configured; the handle
  // is absent when the sender record carries no username.
  owner_tg_user_id?: number
  owner_username?: string
  // Whether the loop can actually DM its owner: a bot cannot open a private
  // chat, so it can only reach an owner who has written to this loop's own
  // bot first (#73).
  owner_dm_ready: boolean
  // Whether the loop is in the fleet channel: it receives what addresses it
  // there and may post to it (ADR-0032 item 2). Always present since #295.
  in_fleet_channel: boolean
}

export interface Turn {
  id: string
  loop_id: string
  session_id: string
  trigger: string
  started_at: number
  ended_at: number
  is_error: boolean
  result_text: string
  cost_usd: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  context_tokens: number
  duration_ms: number
}

export interface LoopEvent {
  id: number
  loop_id: string
  session_id: string
  turn_id: string
  ts: number
  type: string
  subtype: string
  payload: string
}

// The two destinations the composer offers (ADR-0026): the loop's private
// control_room thread, or a post to the shared group conversation.
export type MessageDestination = 'control_room' | 'group'

export interface ChatMessage {
  id: number
  ts: number
  origin: string
  author: string
  from_loop_id?: string
  text: string
  mentions: string[]
  // The loops the hub delivered this to, by id — not by name. Hub delivery,
  // not surface delivery: people on an attached surface are never in it.
  delivered_to: string[]
  conversation: string
  conversation_loop_id?: string
  // A surface send that did not get through, after the retries (#148). Set
  // on the message itself, so a reader of the message sees what a reader of
  // the timeline note sees. Absent on everything that was delivered, and on
  // everything inbound — nobody sent those.
  send_failed_at?: number
  // The surface's own reason, scrubbed of credentials by the sender (#155),
  // which is why it can be shown verbatim.
  send_error?: string
  // When the failure stopped being the operator's business — a retry of this
  // row got through, or they dismissed it (#269). Absent while it is still
  // unresolved, which is what the Undelivered pane and the Fleet count ask
  // for. The failure itself is never erased: `send_failed_at` and
  // `send_error` stay, so what failed and why survives the resolution.
  send_resolved_at?: number
  // What became of it. Three today — 'delivered' (a retry of this row got
  // through), 'dismissed' (the operator is done looking), 'resent' (the loop
  // said the words again itself in a later message, and that one arrived,
  // #270) — and a plain string rather than a union of those three, because
  // the union would be a claim the room decides this field. It does not: the
  // hub writes it, a browser tab outlives an upgrade, and a closed union
  // makes every switch on it look exhaustive to the compiler while the
  // server can still send a fourth. `messages.ts` narrows it in one place so
  // the switches stay exhaustive over something the room does control.
  //
  // Ask this only to say *what became of* the send; to ask whether it is
  // still on the operator's list, read `send_resolved_at`.
  send_resolution?: string
  // Which message carried the words the second time, for a 'resent'
  // resolution; absent on the others (#270). The failure's own row says a
  // resend happened; this says where to read what was actually said.
  send_resent_as?: number
  // The failed message these words were said again for — the mirror of
  // `send_resent_as`, on the message that did the resending. Mirrored for
  // the api.ts/Go sync rule; nothing in the room reads it yet.
  resends_id?: number
  // Whether this message is on the surface too (#285, ADR-0032 item 6):
  // 'not_mirrored' (on the hub only, and staying there — the operator's
  // posts, a loop with no surface), 'pending' (bound for the surface and not
  // there yet), 'mirrored'. A plain string for the reason `send_resolution`
  // is one, narrowed in `messages.ts`. Optional because a server from before
  // #301 omits it, and an absent field is that server, not an answer.
  mirror?: string
  // The message this one explicitly replies to, when it is a reply. In the
  // fleet channel a reply addresses the replied-to loop with no mention
  // needed (ADR-0025); the channel page draws it as a quote (#286).
  reply_to_id?: number
}

export interface TGSender {
  tg_user_id: number
  username: string
  display: string
  status: 'pending' | 'allowed' | 'blocked'
  pair_code: string
  first_seen_via: string
  created_at: number
  updated_at: number
}

// A fleet rule: one instruction injected into every loop's prompt, ahead of
// its mission (ADR pending, #33).
export interface FleetRule {
  id: string
  title: string
  body: string
  enabled: boolean
  created_at: number
  updated_at: number
}

// What the size guard measures and what it allows. section_chars is the
// rendered size of the enabled set — the number the guard itself checks — so
// a panel counting against it can never let through what the API rejects.
export interface RulesBudget {
  section_chars: number
  section_chars_max: number
  title_chars_max: number
  body_chars_max: number
}

export interface RulesView {
  rules: FleetRule[]
  budget: RulesBudget
}

export interface Settings {
  claude_token_set: boolean
  // Whether this hub was started to allow an uncontained loop (--runtime bare
  // or --allow-bare). A property of how the hub was started, not of a loop:
  // New loop offers the choice only where the API would accept it (#255).
  bare_allowed: boolean
  // Context-rotation thresholds (ADR-0022): effective percentages of the
  // model's window, defaults included.
  context_arm_percent: number
  context_force_percent: number
}

// Which build this Spool is, mirrored from internal/version.Info. Served
// unauthenticated, because an operator filing a bug should not need a token
// to say which Spool it was.
export interface VersionInfo {
  version: string
  commit: string
  built_at: string
  go: string
}

export interface LoopSecret {
  name: string
  updated_at: number
}

// What a PATCH did about the loop's running session, mirrored from
// httpapi's rotation constants. A mission is part of the loop's system
// prompt, and a running session cannot be given a new one (#162): saving a
// changed mission asks for a rotation (#261) rather than taking effect where
// the operator is looking.
//
//   none        the mission was not named, or is what it already was
//   queued      a rotation was asked for; it fires at the next quiet boundary
//   no_session  the mission saved, but there was no session to rotate
//
// Always present, so a server that did not rotate and a server too old to say
// cannot read the same — the context_fill_pct lesson (#122).
export type Rotation = 'none' | 'queued' | 'no_session'

// The saved loop, with the one field the loop itself does not have. The
// server embeds the loop rather than wrapping it, so every field a PATCH
// already answered is where it was.
export interface PatchLoopResp extends LoopView {
  rotation: Rotation
}

export interface CreateLoopReq {
  name: string
  mission: string
  model?: string
  effort?: string
  pacing?: 'fixed' | 'self'
  runtime?: 'bare' | 'docker' // create-only; empty = server default
  image?: string // docker only
  mem_mb?: number // docker only
  cpus?: number // docker only
  workspace_path?: string // bare only
  workspace_mode?: 'auto' | 'dir' | 'none'
  tick_interval_sec?: number
  min_wake_sec?: number
  max_wake_sec?: number
  idle_timeout_sec?: number
  // Empty on PATCH detaches the bot (#165); optional on create, where a loop
  // may start with no surface (#287).
  tg_bot_token?: string
  // Whether the loop is in the fleet channel (ADR-0032). On create, absent
  // means the server's default: out for a fleet's first loop, in otherwise.
  in_fleet_channel?: boolean
}

// ApiError carries the API's machine-readable reason alongside its prose, so
// a caller can branch on the cause without parsing the message.
export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, message: string, code = '') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) {
    let msg = `${res.status}`
    let code = ''
    try {
      const body = await res.json()
      if (body.error) msg = body.error
      if (body.code) code = body.code
    } catch {
      /* keep status */
    }
    throw new ApiError(res.status, msg, code)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

// One window of a loop's timeline, live or historical. Exported because the
// page compares a returned page against it to know it has reached the loop's
// first event: a short page means there was nothing more to give.
export const EVENT_WINDOW = 300

export const api = {
  // `runtime` is the kind a loop gets when a create request names none — the
  // hub's own default, which New loop starts the runtime choice on.
  health: () => req<{ ok: boolean; claude_version: string; runtime: string }>('/api/health'),
  version: () => req<VersionInfo>('/api/version'),
  loops: () => req<LoopView[]>('/api/loops'),
  loop: (name: string) => req<LoopView>(`/api/loops/${name}`),
  createLoop: (body: CreateLoopReq) =>
    req<LoopView>('/api/loops', { method: 'POST', body: JSON.stringify(body) }),
  patchLoop: (name: string, body: Partial<CreateLoopReq>) =>
    req<PatchLoopResp>(`/api/loops/${name}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteLoop: (name: string, removeWorktree: boolean) =>
    req<{ deleted: boolean }>(`/api/loops/${name}?remove_worktree=${removeWorktree ? 1 : 0}`, {
      method: 'DELETE',
    }),
  pause: (name: string) => req<LoopView>(`/api/loops/${name}/pause`, { method: 'POST' }),
  resume: (name: string) => req<LoopView>(`/api/loops/${name}/resume`, { method: 'POST' }),
  wake: (name: string) => req<{ woken: boolean }>(`/api/loops/${name}/wake`, { method: 'POST' }),
  kill: (name: string) => req<{ killed: boolean }>(`/api/loops/${name}/kill`, { method: 'POST' }),
  rotate: (name: string) => req<{ rotating: boolean }>(`/api/loops/${name}/rotate`, { method: 'POST' }),
  setOwner: (name: string, tgUserID: number) =>
    req<LoopView>(`/api/loops/${name}/owner`, {
      method: 'PUT',
      body: JSON.stringify({ tg_user_id: tgUserID }),
    }),
  message: (
    name: string,
    text: string,
    destination: MessageDestination = 'control_room',
    author = 'operator',
  ) =>
    req<{ queued: boolean }>(`/api/loops/${name}/message`, {
      method: 'POST',
      body: JSON.stringify({ author, text, destination }),
    }),
  // The newest window, not the oldest: `before_id=0` means "the newest end of
  // the timeline" and the server returns the page oldest-first, so a loop with
  // thousands of events opens on its recent history rather than on the first
  // events of its life (#119). The `after_id` form still exists server-side for
  // following the tail; nothing here needs it, because the stream re-reads this
  // window instead of appending.
  events: (name: string, limit = EVENT_WINDOW) =>
    req<LoopEvent[]>(`/api/loops/${name}/events?before_id=0&limit=${limit}`),
  // The window before an id, for walking back through history (#120). Same
  // endpoint and same page size as the live window; only the cursor differs.
  eventsBefore: (name: string, beforeID: number, limit = EVENT_WINDOW) =>
    req<LoopEvent[]>(`/api/loops/${name}/events?before_id=${beforeID}&limit=${limit}`),
  // The window after an id, oldest-first — for closing a gap when the live
  // window has moved further than the page was watching (#120).
  eventsAfter: (name: string, afterID: number, limit: number) =>
    req<LoopEvent[]>(`/api/loops/${name}/events?after_id=${afterID}&limit=${limit}`),
  turns: (name: string, limit = 50) => req<Turn[]>(`/api/loops/${name}/turns?limit=${limit}`),
  // One loop's failed sends, uncapped — the list its Fleet badge counts, by
  // the same predicate over the same scope (#290), so the two cannot
  // disagree. Activity is a newest-100 window across everything, which is
  // why the badge could once name a number the operator could not find
  // (#263). The route also answers fleet-wide without `loop`; nothing in the
  // room asks for that since the list became a pane of the loop (#281).
  undelivered: (name: string) => req<ChatMessage[]>(`/api/undelivered?loop=${encodeURIComponent(name)}`),
  // 202, not 200: the send is the surface's and is queued behind whatever it
  // is already doing, so the outcome arrives as the row resolving or its
  // error changing rather than in this response (#269). The room must not
  // report it as delivered.
  retrySend: (id: number) => req<{ retrying: boolean }>(`/api/messages/${id}/retry`, { method: 'POST' }),
  // Resolves the failure without sending anything: the operator has read it
  // and is done. Takes the row off their list; it does not rewrite history.
  dismissSend: (id: number) => req<{ dismissed: boolean }>(`/api/messages/${id}/dismiss`, { method: 'POST' }),
  activity: (limit = 100) => req<ChatMessage[]>(`/api/activity?limit=${limit}`),
  // The fleet channel's timeline, newest first like a loop's conversation.
  // It is the hub's rather than any loop's (ADR-0032 item 1), so it is not
  // reached through one.
  group: (limit = 100) => req<ChatMessage[]>(`/api/group?limit=${limit}`),
  // The operator posting to the fleet channel. It wakes the loops the text
  // addresses and no others, and never leaves the hub (ADR-0032 item 4):
  // nobody on an attached surface sees it. 202 — delivery is the loops'.
  // `replyTo` is the fleet-channel message it answers (#311); the server
  // refuses one that is gone (`unknown_reply_to`) or from another
  // conversation (`cross_conversation_reply_to`) with a 400.
  postGroup: (text: string, replyTo?: number) =>
    req<{ queued: boolean }>('/api/group', {
      method: 'POST',
      body: JSON.stringify({ text, reply_to_id: replyTo || undefined }),
    }),
  conversation: (name: string, kind: 'control_room' | 'owner_dm' = 'control_room', limit = 100) =>
    req<ChatMessage[]>(`/api/loops/${name}/conversation?conversation=${kind}&limit=${limit}`),
  telegramStatus: (name: string) =>
    req<{ configured: boolean; bot_username: string; group_bound: boolean; bridge?: unknown }>(
      `/api/loops/${name}/telegram/status`,
    ),
  senders: () => req<TGSender[]>('/api/telegram/senders'),
  allowSender: (id: number) => req<TGSender>(`/api/telegram/senders/${id}/allow`, { method: 'POST' }),
  blockSender: (id: number) => req<TGSender>(`/api/telegram/senders/${id}/block`, { method: 'POST' }),
  deleteSender: (id: number) =>
    req<{ deleted: boolean }>(`/api/telegram/senders/${id}`, { method: 'DELETE' }),
  inspectWorkspace: (path: string) =>
    req<{ path: string; exists: boolean; is_git: boolean }>('/api/workspace/inspect', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),
  // The session (#239). The hub trades its operator token for an HttpOnly
  // cookie, so the room holds a session and never a credential: nothing here
  // can read the cookie back, and an EventSource — which cannot carry a
  // header — authenticates like every other request.
  login: (token: string) => req<void>('/api/login', { method: 'POST', body: JSON.stringify({ token }) }),
  logout: () => req<void>('/api/logout', { method: 'POST' }),
  settings: () => req<Settings>('/api/settings'),
  // The token is write-only: send '' to clear it. Presence comes back in Settings.
  setClaudeToken: (token: string) =>
    req<Settings>('/api/settings', {
      method: 'PUT',
      body: JSON.stringify({ claude_oauth_token: token }),
    }),
  // Both thresholds go in one write: the server validates them as a pair
  // (1-99, arm below force), so sending one alone would be judged against a
  // stored neighbour the operator may be in the middle of changing.
  setRotationThresholds: (arm: number, force: number) =>
    req<Settings>('/api/settings', {
      method: 'PUT',
      body: JSON.stringify({ context_arm_percent: arm, context_force_percent: force }),
    }),
  // Power verbs return the loop post-verb, so the caller can cache the result
  // rather than refetch. All four are 409 on the bare runtime, which has no
  // workstation to power.
  workstationPower: (name: string, verb: 'restart' | 'poweroff' | 'poweron' | 'recreate') =>
    req<LoopView>(`/api/loops/${name}/workstation/${verb}`, { method: 'POST' }),
  rules: () => req<RulesView>('/api/rules'),
  createRule: (body: { title: string; body: string; enabled: boolean }) =>
    req<FleetRule>('/api/rules', { method: 'POST', body: JSON.stringify(body) }),
  patchRule: (id: string, body: Partial<{ title: string; body: string; enabled: boolean }>) =>
    req<FleetRule>(`/api/rules/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteRule: (id: string) => req<void>(`/api/rules/${id}`, { method: 'DELETE' }),
  // Secret values are write-only: the list returns names only.
  loopSecrets: (name: string) => req<LoopSecret[]>(`/api/loops/${name}/secrets`),
  setLoopSecret: (name: string, key: string, value: string) =>
    req<LoopSecret[]>(`/api/loops/${name}/secrets/${key}`, {
      method: 'PUT',
      body: JSON.stringify({ value }),
    }),
  deleteLoopSecret: (name: string, key: string) =>
    req<{ deleted: boolean }>(`/api/loops/${name}/secrets/${key}`, { method: 'DELETE' }),
}
