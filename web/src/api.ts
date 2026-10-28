// Typed client for the Spool REST API.

// A problem the hub's health check found with a loop (ADR-0053), open
// since since (Unix ms) until the signal clears or an admin acknowledges it.
export interface Problem {
  symptom: 'workstation_down' | 'turn_errors' | 'cloud_address' | 'cloud_connections' | string
  since: number
}
export type Health = 'ok' | 'warning' | 'failing'

export type Subagents = 'allowed' | 'off'

export interface LoopView {
  id: string
  name: string
  mission: string
  // What the loop says it is doing (#750), from its last [doing: …]
  // trailer, and when it said so in Unix ms; '' and 0 until it says, or
  // after it clears it. Always present (#751).
  doing: string
  doing_at: number
  // The open problems the hub's health check found with the loop, oldest
  // first, and health summing them up: 'failing' while workstation_down or
  // cloud_address is open, 'warning' while only turn_errors is, 'ok' with
  // none (ADR-0053).
  problems: Problem[]
  health: Health
  model: string
  // The CLI's sentence refusing \`model\`, recorded when a turn failed on the
  // API's 404 (#289); '' when the model is not refused. While it is set the
  // loop is \`model_unrecognized\` and takes no turns; an edit of the model
  // clears it.
  model_refusal: string
  workspace_mode: 'none' | 'dir' | 'worktree'
  workspace_path: string
  repo_path: string
  worktree_path: string
  branch: string
  runtime: 'bare' | 'docker' | 'cloud'
  // The Claude login a contained loop runs on, '' for the default; a bare
  // or cloud loop's is always '' (ADR-0060).
  claude_login: string
  // A cloud loop's current session on claude.ai, absent before its first
  // session; the hub serves only a https://claude.ai/code/ page (ADR-0056).
  cloud_session_url?: string
  // A cloud loop's turn that may be running in a session, absent when the
  // hub knows of none (ADR-0059, #1028).
  cloud_turn?: CloudTurn
  // Set on a cloud loop while the environment's allowlist isn't confirmed
  // (ADR-0059): a warning, never a hold.
  cloud_allowlist_unconfirmed?: boolean
  // A bare loop asked to move into a docker workstation (#632): true from
  // the ask until the move lands or fails, or a hub restart drops it.
  rehoming: boolean
  image: string
  mem_mb: number
  cpus: number
  tick_interval_sec: number
  min_wake_sec: number
  max_wake_sec: number
  idle_timeout_sec: number
  pacing: 'fixed' | 'self'
  effort: string
  // Whether a turn may spawn subagents (#855): 'off' denies Claude Code's
  // Agent tool on every turn. Never 'off' on a cloud loop, whose session's
  // tools the hub has no say over. Absent from a hub too old to set it.
  subagents?: Subagents
  // The loop's own daily USD cap (#652), 0 for none: at or over it the loop
  // sleeps until the hub's local day turns. A cloud loop's spend is the
  // hub's estimate, and the cap counts that. Absent from a hub too old to
  // set it.
  daily_cap_usd?: number
  // Whether that cap holds now, so a capped loop's page can say which cap
  // it is under. True for a loop in its turn too, which the cap takes at
  // the turn's end.
  daily_cap_reached?: boolean
  tg_bot_username: string
  tg_group_chat_id: number
  status: 'active' | 'paused' | 'archived'
  current_session_id: string
  current_pid: number
  created_at: number
  updated_at: number
  // Who made the loop and who last changed it through the API (#695): a
  // user's name, or "operator" for the operator token. Empty on a loop
  // older than the change record.
  created_by: string
  updated_by: string
  // busy, idle, waking, draining, asleep, paused, workstation_off,
  // workstation_down, model_unrecognized (#289) or capped (#650).
  state: string
  next_tick_at: number
  // When a capped loop wakes: the reset that lifts its login's plan cap
  // (#650, #1075), or the hub's next local day for a loop at its own daily cap
  // (#652), whichever is later. 0 while the loop is not capped.
  capped_until: number
  // The known costs of today's turns, a cloud turn's being the hub's
  // estimate from its token usage (ADR-0059). Whether any is an estimate,
  // and how many turns have no cost at all, since unknown is not zero.
  cost_today_usd: number
  cost_today_estimated: boolean
  cost_today_unknown_turns: number
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
  // The model id the CLI reported for the latest turn: what an alias like
  // "opus" resolved to, or the configured id itself. '' before any turn (#289).
  resolved_model: string
  // How many messages this loop sent never reached their surface and that
  // nobody has dealt with: no successful retry, no dismissal (#202, #269). No age
  // limit — a failure stops counting when someone resolves it, not when a
  // clock decides the operator is done looking. The server counts it,
  // because the activity feed the client can see is a newest-N window and
  // would undercount silently on a busy fleet — and a count quietly wrong
  // about a delivery failure reads as a checked zero.
  undelivered: number
  has_tg_token: boolean
  // Which surface the loop is attached to: one at most, '' for none (#230).
  // Optional because a hub from before the Slack surface omits it; read it
  // through `loopSurface` in slack.ts, which falls back to has_tg_token.
  surface?: '' | 'telegram' | 'slack'
  // Whether both Slack tokens are stored. The tokens themselves are
  // write-only, as the Telegram one is.
  has_slack_tokens?: boolean
  workstation_up: boolean
  workstation_detail?: string
  // Why the workstation is down: '' while it is up, 'powered_off' when the
  // operator switched it off, 'not_provisioned' before a wake has built it,
  // 'unauthenticated' when there is no usable Claude login to run under
  // (none is configured, or the API refused the one in use), and
  // 'unreachable' when it should be running and isn't.
  down_reason: '' | 'powered_off' | 'not_provisioned' | 'unauthenticated' | 'unreachable' | 'hub_unreachable'
  // The allowlisted Telegram sender this loop may message privately, and the
  // handle to show for them. Absent when no owner is configured; the handle
  // is absent when the sender record carries no username.
  owner_tg_user_id?: number
  // The Slack owner, by Slack user id (U…), on a Slack loop. owner_username
  // then carries their display name.
  owner_slack_user_id?: string
  owner_username?: string
  // The user linked as the owner, and "" when the owner is a sender allowed
  // before links, who keeps working but is not linked (#731).
  owner_user: string
  // Whether the loop can actually DM its owner: a bot cannot open a private
  // chat, so it can only reach an owner who has written to this loop's own
  // bot first (#73).
  owner_dm_ready: boolean
  // Whether the loop is in the fleet channel: it receives what addresses it
  // there and may post to it (ADR-0032). Always present since #295.
  in_fleet_channel: boolean
  // The connections attached to the loop (ADR-0043), name-sorted, by name
  // and kind only: never a value. Absent from a hub too old to attach.
  connections?: LoopConnection[]
  // What the loop's latest session init in this hub run said it can reach
  // through MCP (#489): the session that init was, every server it reported
  // in its order, and how many MCP tools it has, built-ins and skills left
  // out. All three are absent until a session starts in this hub run.
  mcp_session_id?: string
  mcp_servers?: MCPServer[]
  tool_count?: number
}

// One MCP server as the session's init reported it. Status is the CLI's own
// word: connected, failed, pending, …
export interface MCPServer {
  name: string
  status: string
  tool_count: number
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
  // Per turn and summable (#194). A cloud turn's is the hub's estimate
  // (cost_estimated), and null when the hub can't price its model.
  cost_usd: number | null
  cost_estimated: boolean
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
  // What became of it. Four today — 'delivered' (a retry of this row got
  // through), 'dismissed' (the operator is done looking), 'resent' (the loop
  // said the words again itself in a later message, and that one arrived,
  // #270), 'dismissed_by_loop' (the loop decided not to say it again, #561)
  // — and a plain string rather than a union of those four, because
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
  // The loop was told of this failure as often as it will be, and neither
  // resent nor dismissed it (#561): it is the operator's alone now. Not
  // always reminded: a failure from before migration 0038 was told once, and
  // that migration counts it as told in full. True only while the failure is unresolved; absent otherwise.
  send_left_by_loop?: boolean
  // The failed message these words were said again for — the mirror of
  // `send_resent_as`, on the message that did the resending. Mirrored for
  // the api.ts/Go sync rule; nothing in the room reads it yet.
  resends_id?: number
  // Whether this message is on the surface too (#285, ADR-0032):
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
  // The files that came with it (#460). Absent when there are none.
  attachments?: Attachment[]
  // Its reactions, oldest first (ADR-0040, #535). Absent when there are none.
  reactions?: Reaction[]
  // The ballot it carries if it is a loop's poll (ADR-0041, #554). Absent on
  // every other message.
  poll?: Poll
  // The channel a channel message was posted in: 'group' for the fleet
  // channel. Absent on owner_dm and control_room messages, and on a server
  // from before channels (#483). Mirrored for the api.ts/Go sync rule;
  // nothing in the room reads it yet.
  channel?: string
  // The message this one corrects, when a person edited or deleted what they
  // said on a surface (ADR-0055): this is what they now stand behind, and
  // `edit_kind` says which — 'edited' or 'deleted', a plain string for the
  // reason `send_resolution` is one. A deletion carries no text. Both absent
  // on every other message. `edits.ts` reads them.
  edit_of?: number
  edit_kind?: string
  // When the person last edited this message, and when they deleted it;
  // absent for never. Unix milliseconds. A deletion took the words, so a
  // message with `deleted_at` has an empty `text` (ADR-0055).
  edited_at?: number
  deleted_at?: number
}

// A poll's ballot, as store.Poll has it, with each voter's current choice
// (ADR-0041). The message's text is the question. A retracted vote is not
// in `votes`.
export interface Poll {
  message_id: number
  // Two to ten, in order. A vote names them by index.
  options: string[]
  multiple: boolean
  // When the hub closes it, 0 for one its author closes; when it closed, 0
  // while it is open. Unix milliseconds.
  closes_at: number
  closed_at: number
  // Oldest first.
  votes: Vote[]
}

// One voter's whole choice in one poll, as store.Vote has it.
export interface Vote {
  id: number
  poll_id: number
  // Who voted, keyed as a reactor is: `loop:<id>` or `<surface>:<user id>`.
  voter_key: string
  // The voter's name as it was shown when they voted.
  voter: string
  // The indexes of the options picked, ascending.
  choice: number[]
  ts: number
}

// One reactor's emoji on one hub message, as store.Reaction has it
// (ADR-0040). A reaction is no message of its own: it has no conversation and
// no recipients.
export interface Reaction {
  id: number
  message_id: number
  // Who reacted, the same whichever bot reported it: `loop:<id>` for a loop,
  // `<surface>:<user id>` for a person.
  reactor_key: string
  // The reactor's name as it was shown when they reacted.
  reactor: string
  // The Unicode the surface reported, or :name: for a custom emoji with none.
  emoji: string
  ts: number
}

// A file that came with a message, from a chat surface, a loop's send, or
// the operator's composer (#123). The hub keeps the bytes for 30 days; the
// row outlives them, so a message still says what came with it.
export interface Attachment {
  id: number
  // 0 on a fresh upload, until the message that carries it is sent.
  message_id: number
  name: string
  // "" when the file never arrived.
  mime: string
  kind: 'image' | 'file'
  // Bytes. For a file that never arrived, what the surface declared (may
  // be 0).
  size: number
  // Images only, when the hub could read them.
  width?: number
  height?: number
  // Why the file never arrived: 'too_large', 'fetch_failed', or
  // 'missing_scope' when the surface's app may not read files (#480). A
  // plain string for the reason `send_resolution` is one: the hub writes it.
  not_kept?: string
  created_at: number
  // When retention removed the bytes.
  removed_at?: number
}

// The largest file the hub keeps; the upload route refuses anything bigger
// (`attachment_too_large`), so the composer says so before sending it.
export const MAX_ATTACHMENT_BYTES = 20 * 1024 * 1024

// Where an attachment's bytes are. The session cookie authenticates it, so
// it works as an <img> source and a link. The types in INLINE_IMAGE_TYPES
// are served inline, anything else as a download.
export const attachmentURL = (id: number) => `/api/attachments/${id}`

// The image types the hub serves inline, mirroring `inlineImages` in
// internal/httpapi/attachments.go. Any other image, a BMP or an icon, goes
// out as a download with nosniff, which an <img> will not draw.
export const INLINE_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp'])

export interface TGSender {
  tg_user_id: number
  username: string
  display: string
  status: 'pending' | 'allowed' | 'blocked'
  // The user this sender is linked as, "" for one allowed before links (#731).
  user: string
  pair_code: string
  first_seen_via: string
  created_at: number
  updated_at: number
}

// A Slack sender on the pairing allowlist (#230). The same lifecycle as a
// Telegram sender, keyed by Slack user id within its workspace: being in the
// workspace does not admit anyone.
export interface SlackSender {
  slack_user_id: string
  team_id: string
  username: string
  display: string
  status: 'pending' | 'allowed' | 'blocked'
  // The user this sender is linked as, "" for one allowed before links (#731).
  user: string
  pair_code: string
  first_seen_via: string
  created_at: number
  updated_at: number
}

// A Slack loop's live state (#230). channel_id is the Slack channel the
// loop's fleet room is: the fleet channel binds to the first Slack channel
// an allowed sender writes in with the app invited; an invite alone, or a
// stranger's message, binds nothing (#438). channel_id is '' until
// then. ignored_events counts messages from Slack channels the app has no
// bound room in (#548): the hint that one is waiting on the loop page.
// last_file_error says why Slack last withheld a shared file and what brings
// files back, '' once Slack hands one over again (#480).
export interface SlackStatus {
  configured: boolean
  bot_user_id: string
  bot_name: string
  team_id: string
  team_name: string
  channel_id: string
  channel_name: string
  bridge: {
    connected: boolean
    last_event_at: number
    last_error: string
    last_file_error: string
    ignored_events: number
  }
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

// A channel: a conversation the hub holds, and the loops in it (#483).
// `group` is the fleet channel; it always exists, and its loops are the ones
// with in_fleet_channel set.
export interface Channel {
  name: string
  // "" when unset.
  description: string
  created_at: number
  // Loop names, sorted.
  loops: string[]
}

// A chat on a surface that the loop's bot is in: for now a Telegram group
// (#514). It carries one of the loop's channels, or none until the operator
// binds it. Mirrors roomView in internal/httpapi/rooms.go.
export interface Room {
  surface: string
  // The surface's id for the chat, as text: a Telegram group's is negative,
  // a Slack channel's starts with C (or G for an older private one).
  room_id: string
  // The chat's name as the surface last reported it; "" for a room bound by
  // id before the bot ever heard from it.
  title: string
  // The channel it carries; "" while unbound. The fleet channel's room
  // carries `group`.
  channel: string
  first_seen_at: number
  // 0 while unbound.
  bound_at: number
}

// What a model resolves to on this hub (#332, ADR-0033). resolved is the id
// the model runs as, "" until the hub has one; source says how it learned
// it: "probe" is the hub's zero-token run at start, "turn" a real turn's
// init, which wins. cli_version is the CLI the probe ran, "" for a turn.
export interface ModelResolution {
  model: string
  resolved: string
  source: '' | 'probe' | 'turn'
  cli_version: string
  resolved_at: number
}

// A model id the operator added to the dropdowns. label is optional ("").
export interface CustomModel extends ModelResolution {
  id: string
  label: string
}

// Everything a model dropdown needs. aliases is the families the hub
// offers, in the order it offers them; custom is ordered as added.
export interface ModelList {
  aliases: ModelResolution[]
  custom: CustomModel[]
}

// Settings → Cloud (ADR-0056, #864): the hub's setup for cloud loops, as
// steps the hub checks. Mirrors httpapi's cloudSettingsView.
export interface CloudListenerView {
  on: boolean
  // Where it serves, such as 127.0.0.1:8090; absent while it is off.
  addr?: string
  // Opened by --cloud-listen, so only a restart without the flag closes it.
  from_flag: boolean
}

// Where a failed check stopped. `listener_off` and `refused` made no request:
// the listener is off, or the stored address is one the hub won't accept.
// `session`: the hub's own request passed, but a cloud session's start
// never reached the hub at the address (#1093). It holds until the operator
// checks again, changes the address or switches the listener.
export type CloudCheckStage =
  'listener_off' | 'refused' | 'dns' | 'tls' | 'timeout' | 'unreachable' | 'other_server' | 'session'

export interface CloudCheck {
  state: 'ok' | 'failed'
  stage?: CloudCheckStage
  // `private` on a pass whose request went to a loopback, private, CGNAT,
  // ULA or link-local address: it shows this machine reaches the hub, not
  // that the internet does (#1093).
  route?: 'private'
  // What answered, or how it failed; an admin's alone.
  detail?: string
  checked_at: number
}

export interface CloudSettings {
  listener: CloudListenerView
  // The public address as an HTTPS origin, '' while none is set.
  address: string
  // `quick_tunnel`: a *.trycloudflare.com address, accepted, whose name goes
  // to someone else once the tunnel stops.
  warning?: 'quick_tunnel'
  // The last check of `address`, absent until one has run.
  check?: CloudCheck
  // Whether the account is set, never what it is, and its last check.
  account: CloudAccountView
  // The one environment every cloud loop shares (ADR-0059).
  environment: CloudEnvironmentView
}

// The shared environment the operator made on claude.ai (ADR-0059,
// as #978 amends it), and the list to paste into its network access. The
// hub can't read the environment back, so the operator's confirm is all it
// knows. An allowlist edit or a new address changes `digest`, and another
// ID drops the confirm.
export interface CloudEnvironmentView {
  // env_…, '' while none is chosen.
  id: string
  // Its name on claude.ai, as the hub's list gave it; '' for an ID pasted
  // that the list doesn't show.
  name: string
  // The environment's Allowed domains, one host per line, built from the
  // current allowlist on each request: the public address's host first.
  allowlist: string
  // The allowlist's entries an environment can't carry: a host with a port
  // of its own, or an old `*` entry.
  not_applied: string[]
  // The allowlist's SHA-256, which a confirm names.
  digest: string
  confirmed: boolean
  // Unix ms, present only while confirmed.
  confirmed_at?: number
}

// One of the account's environments, as the hub lists them on its Claude
// login, for the step's pick by name.
export interface CloudEnvironmentChoice {
  id: string
  name: string
}

// Where a failed account check stopped: `no_account` made no call, and
// `refused` is a 401 or 403 from the API, so a sign-in again.
export type CloudAccountStage = 'no_account' | 'refused' | 'unreachable' | 'other'

export interface CloudAccountCheck {
  state: 'ok' | 'failed'
  stage?: CloudAccountStage
  // How it failed; an admin's alone.
  detail?: string
  checked_at: number
}

export interface CloudAccountView {
  set: boolean
  // Whether the hub has a claude CLI to sign in and refresh with (ADR-0058):
  // the workstation image's, or this machine's when there is no image
  // (#952). It looks on every read, so after fetching the image or
  // installing Claude Code a reload shows the sign-in.
  cli: boolean
  // The last account check, absent until one has run.
  check?: CloudAccountCheck
}

// One of the hub's Claude logins (ADR-0060), mirrored from httpapi's
// claudeLoginView: never its tokens. email is the address it signed in
// with, absent when unknown or when the caller is neither its owner nor an
// admin; owner is the user who added it, absent for the one the hub made.
export interface ClaudeLogin {
  name: string
  email?: string
  owner?: string
  signed_in: boolean
  default: boolean
  created_at: number
  // The last login check, absent before the first.
  check?: LoginCheckRecord
}

// How a login check ended, or that it has not yet (store.LoginCheckRecord).
export interface LoginCheckRecord {
  status: 'pending' | 'ok' | 'refused' | 'inconclusive'
  refusal?: string
  at: number
}

// The fleet's workstation egress allowlist (#542, ADR-0028): the built-in
// hosts in groups, each with why it is there, and the operator's own, which
// the hub's one egress proxy takes while the hub runs. An entry is `host` or
// `host:port`, stored trimmed and lowercased; the page adds names only, and
// a port comes from --egress-allow.
export interface EgressGroup {
  reason: string
  hosts: string[]
}

export interface EgressView {
  // False only when the hub has no egress image: no loop's egress is
  // filtered, so the list binds nothing.
  enforced: boolean
  built_in: EgressGroup[]
  // The operator's hosts, in the order they were added.
  extra: string[]
  // The --egress-allow value, present only when it differs from `extra`
  // ([] for an empty flag, absent when the flag wasn't passed):
  // the flag seeds the list on first start, and the store wins after.
  flag?: string[]
  // When `extra` last changed, and when the proxy last loaded the current
  // list (unix ms). `applied_at` is absent while the proxy doesn't hold the
  // current list: before this hub's first docker wake, or after a copy
  // failed or the proxy went away; the next wake copies it.
  changed_at?: number
  applied_at?: number
}

// The Claude plan's usage as the hub last observed it (#647), mirrored from
// planUsageView in internal/httpapi/planusage.go: one Claude login's. With
// nothing observed both windows are null and `unknown` says why.
export interface PlanUsage {
  five_hour: PlanWindow | null
  seven_day: PlanWindow | null
  as_of?: number
  source?: string
  unknown?: string
  // The login's plan cap (#650, #1075): set while a window is over its
  // threshold and the loops on that login sleep at their next quiet
  // boundary; null or absent while nothing is capped.
  cap?: PlanCap | null
  // While a Resume now holds (#650): the cap stays off until this reset.
  resumed_until?: number
}

// GET /api/plan-usage and Resume now's answer (planUsagesView): every
// Claude login's plan usage, the default first and the rest by name
// (ADR-0060), each with its own cap and Resume now hold (#1075). The hub
// also serves the default's at the top level until #1077 removes it; the
// control room reads logins alone (#1173).
export interface PlanUsages {
  logins: LoginPlanUsage[]
}

// One Claude login's plan usage, by its name (loginPlanUsageView).
export interface LoginPlanUsage extends PlanUsage {
  name: string
}

export type PlanWindowKey = 'five_hour' | 'seven_day'

export interface PlanCap {
  // The windows over their thresholds.
  windows: PlanWindowKey[]
  // When the loops wake: the latest reset among those windows.
  until: number
}

// One limit window. used_percent can run past 100. A window whose reset has
// passed since the observation reads `reset`, with nothing used and no next
// reset time, which only the next observation can tell.
export interface PlanWindow {
  used_percent: number
  resets_at?: number
  reset?: boolean
  // The window's cap threshold as a percentage; 0 or absent when its cap
  // is off (#650).
  cap_percent?: number
}

export interface Settings {
  claude_token_set: boolean
  // Whether this hub was started to allow an uncontained loop (--runtime bare
  // or --allow-bare). A property of how the hub was started, not of a loop:
  // New loop offers the choice only where the API would accept it (#255).
  bare_allowed: boolean
  // The runtime a loop gets when a create request names none — the hub's own
  // default, which New loop starts the runtime choice on (#255).
  default_runtime: 'bare' | 'docker'
  // The `claude` CLI this hub found at start; '' when it found none. Served
  // here, behind the credential, because it describes the host (#258).
  claude_version: string
  // Context-rotation thresholds (ADR-0022): effective percentages of the
  // model's window, defaults included.
  context_arm_percent: number
  context_force_percent: number
  // The plan cap's thresholds (#650): a percentage of each window, 90 by
  // default; 0 turns that window's cap off.
  plan_cap_five_hour_percent: number
  plan_cap_seven_day_percent: number
  // Errored turns in a row that open a turn_errors problem (ADR-0053), 0 to
  // 100; 0 turns that symptom off, and 3 is the default.
  health_error_turns: number
}

// The session's user (#582, #675). A session made with the operator token
// has no user: it acts as the owner, with an empty name and `via: 'token'`.
// While must_change_password holds, the hub answers every other route with
// 403 `password_change_required`, so the room shows only the change page.
export type Role = 'owner' | 'admin' | 'member'
export interface Me {
  name: string
  role: Role
  must_change_password: boolean
  via: 'password' | 'token'
}

// Who the signed-in user is on a chat surface (ADR-0050), mirrored from the
// hub's link view (#703). id is the user's id on the surface; username and
// display are what the surface last said of them, '' when it never did.
export interface UserLink {
  surface: 'telegram' | 'slack'
  id: string
  username: string
  display: string
  created_at: number
}

// A link the signed-in user waits for (ADR-0051): a code of theirs reached
// a loop's bot or app, which asked its sender there to reply YES. loop is the
// loop it reached, and expires_at when the question lapses.
export interface WaitingLink {
  surface: UserLink['surface']
  id: string
  username: string
  display: string
  loop: string
  expires_at: number
}

// One of a loop's private conversations (#720): with its owner on
// owner_dm, or with another allowed person on dm:<name> (ADR-0049). name is
// the person as the identity catalog names them, and person_id their id on
// the surface, which outlives a rename; last_at is the newest message in it.
export interface PrivateConversation {
  conversation: 'owner_dm' | `dm:${string}`
  name: string
  person_id: string
  last_at: number
  // The Spool user the person is linked as (#1111); absent for one no user
  // is, and from a hub before #1111.
  user?: string
}

// A link code, answered once (#703). It links whoever sends it to a loop's
// bot or app in a DM on the surface it was asked for (ADR-0051), once, until
// expires_at.
export interface LinkCode {
  code: string
  expires_at: number
}

// A user of this hub, as an owner's Users section lists them (#691), mirrored
// from the hub half's user view (#690). must_change_password is a user still
// on the one-time password they were given: they have not signed in yet.
export interface HubUser {
  name: string
  role: Role
  must_change_password: boolean
  created_at: number
}

// A user as an owner's Settings → Access lists them (#732), mirrored from
// the hub's accessUserView (#744): with the links they hold, and whether a
// sender one of those links holds is blocked, which no bot answers.
export interface AccessUser extends HubUser {
  links: UserLink[]
  blocked: boolean
}

// The strangers one surface ignored in the window, mirrored from the hub's
// ignoredView (#744): their ids, the most recent first, and whether there
// were more than the hub keeps. paused_until is when strangers' link codes
// there are checked again, in Unix ms, and 0 when they are not paused
// (#789).
export interface IgnoredSurface {
  surface: UserLink['surface']
  count: number
  ids: string[]
  more: boolean
  paused_until: number
}

export interface IgnoredStrangers {
  window_hours: number
  surfaces: IgnoredSurface[]
}

// A user with their one-time password, in the one answer that carries it:
// adding a user or resetting one. The hub keeps only its hash, so the room
// never sees it again.
export interface IssuedUser extends HubUser {
  one_time_password: string
}

// One change a user or the operator token made to a loop through the API,
// mirrored from httpapi's changeView (#695). by is the user's name, or
// "operator" for the token; loop is the loop's name as it was then, so a
// change outlives a deleted or renamed loop. action is one of store's
// LoopChange* names.
export interface LoopChange {
  id: number
  at: number
  by: string
  action: string
  loop: string
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

// Onboarding readiness (#580): one live fact per pillar, and whether the
// first-run page is finished. `completed` is the hub's memory: set the
// first time every pillar is done, or when the surface is skipped with the
// other two done (#797, #798), and cleared when the last loop is deleted.
// The pillars stay live, so a revoked token reads not done without bringing
// the first-run page back. Mirrors httpapi's onboarding view.
export interface OnboardingPillar {
  done: boolean
  // Why, in a few words: what is missing, or for some done pillars what made
  // them so (a bare runtime's harness uses the host's login).
  reason?: string
  // The harness's login check (ADR-0044) is running: set from the moment a
  // check starts until it answers, so a "Check now" stays busy that long.
  checking?: boolean
  // What the hub is doing for the pillar now, while it is not done (#662):
  // a loop's first wake building its workstation, spawning, or on its first
  // turn; a loop answering the operator's first message; a login check.
  // Absent while nothing is in flight. A code this build does not know
  // reads as absent.
  progress?: PillarProgress | (string & {})
  // The loop that progress is about, when it is about one.
  progress_loop?: string
}

export type PillarProgress = 'building_workstation' | 'waking' | 'first_turn' | 'answering' | 'checking'

export interface Onboarding {
  completed: boolean
  harness: OnboardingPillar
  surface: OnboardingPillar
  loops: OnboardingPillar
}

// A connection (ADR-0043): an org-level env variable or tool config, defined
// once under a name and attachable to loops (#572). An attached env-var is
// set in its loop's env from its next turn (#640); an mcp-server reaches its loop
// with #505. A github-app (#887) holds an app's private key, which never
// leaves the hub: a loop holding one fetches hour-long installation tokens
// from it. Mirrors httpapi's connectionView. The secret is write-only: a
// create sends it, and no response carries it — only whether there is one.
export type ConnectionKind = 'env-var' | 'mcp-server' | 'github-app'
export type MCPTransport = 'http' | 'stdio'

// Which fields are set is the kind's: `env` for an env-var;
// `transport`, then `url` or `command` with `args`, for an mcp-server;
// `app_id`, the App ID or Client ID, and `installation_id` for a
// github-app. Read back in full, so nothing secret belongs in it. Empty
// fields are omitted.
export interface ConnectionConfig {
  env?: string
  transport?: MCPTransport
  url?: string
  command?: string
  args?: string[]
  app_id?: string
  installation_id?: number
}

export interface Connection {
  name: string
  kind: ConnectionKind
  // What it points at, as stored. Absent from a member's read (#924): a
  // credential put in a URL or an argument would otherwise reach every
  // member, so only an admin or an owner reads it.
  config?: ConnectionConfig
  has_secret: boolean
  created_at: number
  // The loops it is attached to, by name, sorted. A connection with any is
  // refused deletion (`connection_attached`) until it is detached. Absent
  // from a hub too old to attach.
  loops?: string[]
  // The loop it is private to (#600), absent when the fleet shares it. A
  // private connection is attached to its owner alone: attaching it anywhere
  // else is refused (`connection_private`) until it is shared, one way, and
  // deleting it detaches it from its owner in the same step.
  owner_loop?: string
  // When its value was last replaced (#609), absent if never. The old value
  // is never readable again, and stays redacted.
  rotated_at?: number
  // When it was revoked (#610), absent unless it was. A revoked connection
  // is on no loop and can't be attached, shared or given a new value; it can
  // only be deleted, and its value stays redacted.
  revoked_at?: number
}

// One change to a connection, on its append-only record (#606). Names, not
// references: a row outlives the connection and the loop it names, and the
// loop is named as it was then. `loop` is the loop the change was to: on
// attach and detach, and on share, delete and revoke of a private one, its
// owner. Absent on create, which a private one's owner follows as an
// attach, on rotate, and on delete and revoke of a shared one. A loop's
// listing holds the rows that name it.
export type ConnectionAction = 'create' | 'delete' | 'attach' | 'detach' | 'share' | 'rotate' | 'revoke'

export interface ConnectionEvent {
  action: ConnectionAction
  connection: string
  loop?: string
  at: number
}

// A cloud loop's turn that may be running in a session (ADR-0059). The CLI
// can't interrupt a session, so a stop, a kill or a rotation leaves the turn
// running out of the hub's hearing: cut_off. since is the hub's send, which a
// turn about to start shares with one running; until is when the hub stops
// counting it. session_url is the turn's own session, which after a rotation
// is not the loop's current one. Times are Unix ms.
export interface CloudTurn {
  since: number
  until: number
  session_url: string
  cut_off: boolean
}

// A connection as a loop's view lists it: what it is, never what it holds.
export interface LoopConnection {
  name: string
  kind: ConnectionKind
}

export interface CreateConnectionReq {
  // Optional with `owner_loop`: the hub names a private one itself (#600).
  name?: string
  kind: ConnectionKind
  config: ConnectionConfig
  secret?: string
  // Makes it private to this loop, attached to it on creation (#600).
  owner_loop?: string
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
  subagents?: Subagents
  daily_cap_usd?: number // 0 = no cap; 0 to 100000
  // Create-only; empty = server default. `cloud` is refused with 409
  // `cloud_not_ready` until Settings → Cloud is green.
  runtime?: 'bare' | 'docker' | 'cloud'
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
  // A Slack app is one pair, so the two go together: both set attaches or
  // rotates, both '' detaches, and one alone is a 400 (#230).
  slack_app_token?: string
  slack_bot_token?: string
  // Whether the loop is in the fleet channel (ADR-0032). On create, absent
  // means the server's default: out for a fleet's first loop, in otherwise.
  in_fleet_channel?: boolean
  // The Claude login a contained loop runs on (ADR-0060): absent or
  // `default` for the default. A bare or cloud loop naming another is
  // refused 409 `claude_login`, and a login the hub doesn't hold 400.
  claude_login?: string
}

// The answer to a rehome: the host workspace the move leaves behind ('' for
// a loop with none), and what the workstation won't have.
export interface Rehome {
  rehoming: boolean
  left_behind: string
  not_carried: string[]
}

// ApiError carries the API's machine-readable reason alongside its prose, so
// a caller can branch on the cause without parsing the message.
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  // Seconds until a throttled request may be tried again (429 `throttled`,
  // #675); 0 when the refusal names no wait.
  readonly retryAfter: number

  constructor(status: number, message: string, code = '', retryAfter = 0) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
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
    let retryAfter = 0
    try {
      const body = await res.json()
      if (body.error) msg = body.error
      if (body.code) code = body.code
      if (typeof body.retry_after === 'number') retryAfter = body.retry_after
    } catch {
      /* keep status */
    }
    throw new ApiError(res.status, msg, code, retryAfter)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

// One window of a loop's timeline, live or historical. Exported because the
// page compares a returned page against it to know it has reached the loop's
// first event: a short page means there was nothing more to give.
export const EVENT_WINDOW = 300

export const api = {
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
  // Clears the loop's open problem for symptom as the operator's doing
  // (ADR-0053); nothing open changes nothing. Answers with the loop view.
  acknowledge: (name: string, symptom: string) =>
    req<LoopView>(`/api/loops/${name}/problems/${symptom}/acknowledge`, { method: 'POST' }),
  rotate: (name: string) => req<{ rotating: boolean }>(`/api/loops/${name}/rotate`, { method: 'POST' }),
  // Moves a bare loop into a docker workstation at its next rotation
  // (#624); asking again while it waits answers 202 again. 409 when the
  // loop is not bare or has no running actor; 400 when
  // docker is unavailable, or `loop_listener_unreachable` (ApiError.code)
  // when a workstation could not reach the hub.
  rehome: (name: string) => req<Rehome>(`/api/loops/${name}/rehome`, { method: 'POST' }),
  // An owner is a user, by their link on the loop's surface (#731): 400
  // `no_link` (ApiError.code) for a user with none, `unknown_user` for a
  // name that is no user.
  setOwner: (name: string, user: string) =>
    req<LoopView>(`/api/loops/${name}/owner`, {
      method: 'PUT',
      body: JSON.stringify({ user }),
    }),
  message: (
    name: string,
    text: string,
    destination: MessageDestination = 'control_room',
    attachmentID?: number,
  ) =>
    req<{ queued: boolean }>(`/api/loops/${name}/message`, {
      method: 'POST',
      body: JSON.stringify({
        author: 'operator',
        text,
        destination,
        attachment_id: attachmentID || undefined,
      }),
    }),
  // The first of a send's two steps: the raw bytes, kept by the hub for an
  // hour until a message names the returned id as its `attachment_id`. The
  // one route whose body is not JSON.
  uploadAttachment: (file: File) =>
    req<Attachment>(`/api/attachments?name=${encodeURIComponent(file.name)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: file,
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
  // The loops' change record, newest first (#695); loop narrows it to one.
  changes: (loop?: string, limit = 100) =>
    req<LoopChange[]>(
      `/api/changes?${new URLSearchParams({ ...(loop ? { loop } : {}), limit: String(limit) })}`,
    ),
  // The fleet channel's timeline, newest first like a loop's conversation.
  // It is the hub's rather than any loop's (ADR-0032), so it is not
  // reached through one.
  group: (limit = 100) => req<ChatMessage[]>(`/api/group?limit=${limit}`),
  // The operator posting to the fleet channel. It wakes the loops the text
  // addresses and no others, and never leaves the hub (ADR-0032):
  // nobody on an attached surface sees it. 202 — delivery is the loops'.
  // `replyTo` is the fleet-channel message it answers (#311); the server
  // refuses one that is gone (`unknown_reply_to`) or from another
  // conversation (`cross_conversation_reply_to`) with a 400.
  postGroup: (text: string, replyTo?: number, attachmentID?: number) =>
    req<{ queued: boolean }>('/api/group', {
      method: 'POST',
      body: JSON.stringify({
        text,
        reply_to_id: replyTo || undefined,
        attachment_id: attachmentID || undefined,
      }),
    }),
  conversation: (
    name: string,
    kind: 'control_room' | PrivateConversation['conversation'] = 'control_room',
    limit = 100,
  ) =>
    req<ChatMessage[]>(
      `/api/loops/${name}/conversation?conversation=${encodeURIComponent(kind)}&limit=${limit}`,
    ),
  // The loop's private conversations the caller may read (#720), newest
  // first: an admin's are all of them, a member's the ones that are theirs.
  // A reader gets an empty thread, not an error, for one that is not theirs.
  privateConversations: (name: string) => req<PrivateConversation[]>(`/api/loops/${name}/conversations`),
  telegramStatus: (name: string) =>
    req<{ configured: boolean; bot_username: string; group_bound: boolean; bridge?: unknown }>(
      `/api/loops/${name}/telegram/status`,
    ),
  rooms: (name: string) => req<Room[]>(`/api/loops/${name}/rooms`),
  // Binds a room to one of the loop's channels. A room the bot never heard
  // from is added bound, which is the paste-the-id fallback.
  bindRoom: (name: string, body: { surface: string; room_id: string; channel: string }) =>
    req<Room>(`/api/loops/${name}/rooms`, { method: 'PUT', body: JSON.stringify(body) }),
  forgetRoom: (name: string, surface: string, roomID: string) =>
    req<void>(`/api/loops/${name}/rooms/${surface}/${encodeURIComponent(roomID)}`, { method: 'DELETE' }),
  senders: () => req<TGSender[]>('/api/telegram/senders'),
  allowSender: (id: number) => req<TGSender>(`/api/telegram/senders/${id}/allow`, { method: 'POST' }),
  blockSender: (id: number) => req<TGSender>(`/api/telegram/senders/${id}/block`, { method: 'POST' }),
  deleteSender: (id: number) =>
    req<{ deleted: boolean }>(`/api/telegram/senders/${id}`, { method: 'DELETE' }),
  slackStatus: (name: string) => req<SlackStatus>(`/api/loops/${name}/slack/status`),
  slackSenders: () => req<SlackSender[]>('/api/slack/senders'),
  allowSlackSender: (id: string) =>
    req<SlackSender>(`/api/slack/senders/${encodeURIComponent(id)}/allow`, { method: 'POST' }),
  blockSlackSender: (id: string) =>
    req<SlackSender>(`/api/slack/senders/${encodeURIComponent(id)}/block`, { method: 'POST' }),
  deleteSlackSender: (id: string) =>
    req<{ deleted: boolean }>(`/api/slack/senders/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  inspectWorkspace: (path: string) =>
    req<{ path: string; exists: boolean; is_git: boolean }>('/api/workspace/inspect', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),
  // The session (#239). The hub trades its operator token for an HttpOnly
  // cookie, so the room holds a session and never a credential: nothing here
  // can read the cookie back, and an EventSource — which cannot carry a
  // header — authenticates like every other request.
  // A user signs in with a username and password (#582); the token form is
  // kept for one release. Either way the answer is the session's user.
  login: (credentials: { username: string; password: string } | { token: string }) =>
    req<Me>('/api/login', { method: 'POST', body: JSON.stringify(credentials) }),
  logout: () => req<void>('/api/logout', { method: 'POST' }),
  me: () => req<Me>('/api/me'),
  // The signed-in user's links, at most one per surface (#703). Asking for a
  // code replaces any open one; the operator token is no user, and gets 400
  // no_user for a code or a removal.
  myLinks: () => req<UserLink[]>('/api/me/links'),
  myWaitingLinks: () => req<WaitingLink[]>('/api/me/links/waiting'),
  linkCode: (surface: UserLink['surface']) =>
    req<LinkCode>('/api/me/links/code', { method: 'POST', body: JSON.stringify({ surface }) }),
  removeLink: (surface: UserLink['surface']) => req<void>(`/api/me/links/${surface}`, { method: 'DELETE' }),
  // The answer is the session's user with the flag cleared; the session that
  // changed it stays signed in, and the user's others are revoked.
  changePassword: (body: { new_password: string; current_password?: string }) =>
    req<Me>('/api/me/password', { method: 'POST', body: JSON.stringify(body) }),
  // Managing users is an owner's alone (#690). Granting admin or owner, by
  // adding a user with that role or raising one to it, lowering or removing
  // an owner (#923), and resetting anyone's password take the acting owner's
  // password again, or the operator token for a token session: 403
  // `confirm_password` when it is missing or wrong. The last owner can't be
  // lowered or removed: 409 `last_owner`.
  users: () => req<AccessUser[]>('/api/users'),
  // Another user's link, removed as removing your own removes it: the
  // person is a stranger again (#744).
  removeUserLink: (name: string, surface: UserLink['surface']) =>
    req<void>(`/api/users/${encodeURIComponent(name)}/links/${surface}`, { method: 'DELETE' }),
  // Every sender the user's links hold, blocked or allowed again; a user
  // with no link gets 400 no_link.
  blockUser: (name: string) =>
    req<AccessUser>(`/api/users/${encodeURIComponent(name)}/block`, { method: 'POST' }),
  unblockUser: (name: string) =>
    req<AccessUser>(`/api/users/${encodeURIComponent(name)}/unblock`, { method: 'POST' }),
  strangers: () => req<IgnoredStrangers>('/api/strangers'),
  // Strangers' link codes on the surface are checked again at once (#789).
  liftLinkPause: (surface: UserLink['surface']) =>
    req<IgnoredStrangers>(`/api/strangers/${surface}/pause`, { method: 'DELETE' }),
  addUser: (body: { name: string; role: Role; current_password?: string }) =>
    req<IssuedUser>('/api/users', { method: 'POST', body: JSON.stringify(body) }),
  // Resetting ends the user's sessions, as removing does, and always takes
  // the acting owner's password again: a reset hands the owner a way into
  // that account.
  resetUser: (name: string, currentPassword: string) =>
    req<IssuedUser>(`/api/users/${encodeURIComponent(name)}/reset`, {
      method: 'POST',
      body: JSON.stringify({ current_password: currentPassword }),
    }),
  setUserRole: (name: string, body: { role: Role; current_password?: string }) =>
    req<HubUser>(`/api/users/${encodeURIComponent(name)}`, { method: 'PATCH', body: JSON.stringify(body) }),
  removeUser: (name: string, currentPassword?: string) =>
    req<void>(`/api/users/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      ...(currentPassword !== undefined
        ? { body: JSON.stringify({ current_password: currentPassword }) }
        : {}),
    }),
  settings: () => req<Settings>('/api/settings'),
  planUsage: () => req<PlanUsages>('/api/plan-usage'),
  // One count; 0 is off (ADR-0053).
  setHealthErrorTurns: (turns: number) =>
    req<Settings>('/api/settings', { method: 'PUT', body: JSON.stringify({ health_error_turns: turns }) }),
  // Both thresholds go in one write, as the rotation pair does; 0 is off.
  setPlanCaps: (fiveHour: number, sevenDay: number) =>
    req<Settings>('/api/settings', {
      method: 'PUT',
      body: JSON.stringify({ plan_cap_five_hour_percent: fiveHour, plan_cap_seven_day_percent: sevenDay }),
    }),
  // Lifts a login's cap until the next reset of the windows that set it,
  // without touching the thresholds (#650, #1075): the default's with no
  // login named. Answers with the plan read.
  resumePlanCap: (login?: string) =>
    req<PlanUsages>('/api/plan-cap/resume', {
      method: 'POST',
      body: login === undefined ? undefined : JSON.stringify({ claude_login: login }),
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
  onboarding: () => req<Onboarding>('/api/onboarding'),
  // Starts a login check and answers with the readiness it leaves; 409
  // `no_setup_token` on a docker hub with no token to check.
  checkHarness: () => req<Onboarding>('/api/onboarding/harness-check', { method: 'POST' }),
  // Finishes the first-run page with no chat surface (#797, #798): refused
  // 409 while the harness or loops pillar is not done.
  skipSurface: () => req<Onboarding>('/api/onboarding/skip-surface', { method: 'POST' }),
  // Connections, by name as the server lists them. Refusals carry a
  // `connection_*` code (ApiError.code).
  connections: () => req<Connection[]>('/api/connections'),
  createConnection: (body: CreateConnectionReq) =>
    req<Connection>('/api/connections', { method: 'POST', body: JSON.stringify(body) }),
  deleteConnection: (name: string) =>
    req<{ deleted: boolean }>(`/api/connections/${name}`, { method: 'DELETE' }),
  // Both 204 and idempotent: attaching twice, or detaching what is not
  // attached, changes nothing.
  attachConnection: (loop: string, connection: string) =>
    req<void>(`/api/loops/${loop}/connections/${connection}`, { method: 'PUT' }),
  detachConnection: (loop: string, connection: string) =>
    req<void>(`/api/loops/${loop}/connections/${connection}`, { method: 'DELETE' }),
  // One way: a shared value may already be in another loop's env (#600).
  shareConnection: (name: string) => req<Connection>(`/api/connections/${name}/share`, { method: 'POST' }),
  // Replaces the value (#609): every loop holding the connection ends its
  // session with a handoff and wakes with the new one. A github-app's new
  // key ends no session, since no loop held the old one (#887).
  rotateConnection: (name: string, value: string) =>
    req<Connection>(`/api/connections/${name}/secret`, { method: 'PUT', body: JSON.stringify({ value }) }),
  // Takes it from every loop, for good (#610). Attaching it after is refused
  // (`connection_revoked`).
  revokeConnection: (name: string) => req<Connection>(`/api/connections/${name}/revoke`, { method: 'POST' }),
  // The record, newest first, readable by name after what it names is gone.
  connectionEvents: (name: string) => req<ConnectionEvent[]>(`/api/connections/${name}/events`),
  loopConnectionEvents: (loop: string) => req<ConnectionEvent[]>(`/api/loops/${loop}/connection-events`),
  rules: () => req<RulesView>('/api/rules'),
  createRule: (body: { title: string; body: string; enabled: boolean }) =>
    req<FleetRule>('/api/rules', { method: 'POST', body: JSON.stringify(body) }),
  patchRule: (id: string, body: Partial<{ title: string; body: string; enabled: boolean }>) =>
    req<FleetRule>(`/api/rules/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteRule: (id: string) => req<void>(`/api/rules/${id}`, { method: 'DELETE' }),
  // `group` first, then by name.
  channels: () => req<Channel[]>('/api/channels'),
  // One channel's messages, newest first; a deleted channel's still read.
  channelMessages: (name: string, limit = 100) =>
    req<ChatMessage[]>(`/api/channels/${name}/messages?limit=${limit}`),
  // The operator posting into a channel: as postGroup, which it is for
  // `group`. The reply must be from the same channel.
  postChannel: (name: string, text: string, replyTo?: number, attachmentID?: number) =>
    req<{ queued: boolean }>(`/api/channels/${name}/messages`, {
      method: 'POST',
      body: JSON.stringify({
        text,
        reply_to_id: replyTo || undefined,
        attachment_id: attachmentID || undefined,
      }),
    }),
  createChannel: (body: { name: string; description: string }) =>
    req<Channel>('/api/channels', { method: 'POST', body: JSON.stringify(body) }),
  patchChannel: (name: string, body: { description: string }) =>
    req<Channel>(`/api/channels/${name}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteChannel: (name: string) => req<void>(`/api/channels/${name}`, { method: 'DELETE' }),
  // Both idempotent. On `group` these are the same write as PATCHing the
  // loop's in_fleet_channel.
  addChannelLoop: (channel: string, loop: string) =>
    req<void>(`/api/channels/${channel}/loops/${loop}`, { method: 'PUT' }),
  removeChannelLoop: (channel: string, loop: string) =>
    req<void>(`/api/channels/${channel}/loops/${loop}`, { method: 'DELETE' }),
  models: () => req<ModelList>('/api/models'),
  addCustomModel: (body: { model: string; label: string }) =>
    req<CustomModel>('/api/models/custom', { method: 'POST', body: JSON.stringify(body) }),
  relabelCustomModel: (id: string, label: string) =>
    req<CustomModel>(`/api/models/custom/${id}`, { method: 'PATCH', body: JSON.stringify({ label }) }),
  deleteCustomModel: (id: string) => req<void>(`/api/models/custom/${id}`, { method: 'DELETE' }),
  cloudSettings: () => req<CloudSettings>('/api/settings/cloud'),
  // Opens the listener where `listen` names, the hub's default when empty,
  // or closes it. Refused with 409 `cloud_listener`: a bind failure, another
  // address while it is open, or closing the flag's listener.
  switchCloudListener: (on: boolean, listen = '') =>
    req<CloudSettings>('/api/settings/cloud/listener', {
      method: 'PUT',
      body: JSON.stringify({ on, listen }),
    }),
  // Stores the address and checks it at once. Refused with 400
  // `cloud_address_invalid` unless it is an HTTPS origin.
  setCloudAddress: (address: string) =>
    req<CloudSettings>('/api/settings/cloud/address', { method: 'PUT', body: JSON.stringify({ address }) }),
  // The account's environments, read on the hub's Claude login through an
  // API Anthropic doesn't publish: a 502 `cloud_environments_unlisted` when
  // it fails, and the step falls back to pasting the ID.
  cloudEnvironments: () =>
    req<{ environments: CloudEnvironmentChoice[] }>('/api/settings/cloud/environments'),
  // Refused with 400 `cloud_environment_invalid` unless it is env_ plus
  // letters and digits, and `cloud_environment_unlisted` for an ID the
  // account's list doesn't show while that list can be read: a pasted ID is
  // taken only when it can't. The name is the hub's, from its list.
  setCloudEnvironment: (id: string) =>
    req<CloudSettings>('/api/settings/cloud/environment', { method: 'PUT', body: JSON.stringify({ id }) }),
  // Refused with 409 `cloud_allowlist_stale` for a digest that isn't the
  // current list's, or with no environment chosen.
  confirmCloudEnvironment: (digest: string) =>
    req<CloudSettings>('/api/settings/cloud/environment/confirm', {
      method: 'POST',
      body: JSON.stringify({ digest }),
    }),
  clearCloudAddress: () => req<CloudSettings>('/api/settings/cloud/address', { method: 'DELETE' }),
  checkCloudAddress: () => req<CloudSettings>('/api/settings/cloud/check', { method: 'POST' }),
  // The hub's Claude logins (ADR-0060): any user lists them and adds one of
  // their own; its owner or an admin signs it in, checks it, signs it out and
  // removes it, the default included (#1173). Removal is refused (409
  // `claude_login`) while loops run on the login, and the refusal names them.
  // The sign-in is the CLI's own login, run by the hub (#864): it answers the
  // claude.ai link to open, and the code claude.ai then shows finishes it. A
  // sign-in is refused with `cloud_sign_in`, a code with 400 when the CLI
  // didn't take it, which ends that sign-in, and 409 when none is waiting,
  // one ended after ten minutes included.
  claudeLogins: () => req<{ logins: ClaudeLogin[] }>('/api/claude-logins'),
  addClaudeLogin: (name: string) =>
    req<ClaudeLogin>('/api/claude-logins', { method: 'POST', body: JSON.stringify({ name }) }),
  removeClaudeLogin: (name: string) =>
    req<void>(`/api/claude-logins/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  startClaudeLoginSignIn: (name: string) =>
    req<{ link: string }>(`/api/claude-logins/${encodeURIComponent(name)}/sign-in`, { method: 'POST' }),
  finishClaudeLoginSignIn: (name: string, code: string) =>
    req<ClaudeLogin>(`/api/claude-logins/${encodeURIComponent(name)}/sign-in/code`, {
      method: 'POST',
      body: JSON.stringify({ code }),
    }),
  checkClaudeLogin: (name: string) =>
    req<ClaudeLogin>(`/api/claude-logins/${encodeURIComponent(name)}/check`, { method: 'POST' }),
  signOutClaudeLogin: (name: string) =>
    req<ClaudeLogin>(`/api/claude-logins/${encodeURIComponent(name)}/sign-out`, { method: 'POST' }),
  egress: () => req<EgressView>('/api/settings/egress'),
  // Refused with 400 `egress_host_invalid` and egress.Validate's reason, a
  // port included; a host already on the list, once normalized, changes
  // nothing. Removal takes the entry exactly as listed.
  addEgressHost: (host: string) =>
    req<EgressView>('/api/settings/egress/hosts', { method: 'POST', body: JSON.stringify({ host }) }),
  removeEgressHost: (host: string) =>
    req<EgressView>(`/api/settings/egress/hosts/${encodeURIComponent(host)}`, { method: 'DELETE' }),
}
