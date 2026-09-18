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
  // Context occupancy: what the latest turn carried into the model, against
  // that model's window. The limit is 0 when the model is unknown to Spool,
  // in which case there is no honest percentage to show.
  context_tokens: number
  context_limit_tokens: number
  // Occupancy as a percentage of the window, computed by the server so the
  // gauge and rotation judge the same number (0 = unmeasured).
  context_fill_pct: number
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
  delivered_to: string[]
  conversation: string
  conversation_loop_id?: string
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
  // Context-rotation thresholds (ADR-0022): effective percentages of the
  // model's window, defaults included.
  context_arm_percent: number
  context_force_percent: number
}

export interface LoopSecret {
  name: string
  updated_at: number
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
  tg_bot_token?: string
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
  health: () => req<{ ok: boolean; claude_version: string }>('/api/health'),
  loops: () => req<LoopView[]>('/api/loops'),
  loop: (name: string) => req<LoopView>(`/api/loops/${name}`),
  createLoop: (body: CreateLoopReq) =>
    req<LoopView>('/api/loops', { method: 'POST', body: JSON.stringify(body) }),
  patchLoop: (name: string, body: Partial<CreateLoopReq>) =>
    req<LoopView>(`/api/loops/${name}`, { method: 'PATCH', body: JSON.stringify(body) }),
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
  activity: (limit = 100) => req<ChatMessage[]>(`/api/activity?limit=${limit}`),
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
  settings: () => req<Settings>('/api/settings'),
  // The token is write-only: send '' to clear it. Presence comes back in Settings.
  setClaudeToken: (token: string) =>
    req<Settings>('/api/settings', {
      method: 'PUT',
      body: JSON.stringify({ claude_oauth_token: token }),
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
