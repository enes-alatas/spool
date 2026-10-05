// The Slack surface's control-room logic that does not need a component
// (#383), against #230's contract: which surface a loop is on, when a token
// pair can be sent, and what each of the attach errors says.
import { ApiError, LoopView, SlackSender, SlackStatus } from './api'

export type Surface = '' | 'telegram' | 'slack'

// The loop's surface. A hub from before #230 sends no `surface`, and on one
// of those a stored bot token is the only surface there can be.
export function loopSurface(loop: Pick<LoopView, 'surface' | 'has_tg_token'>): Surface {
  if (loop.surface !== undefined) return loop.surface
  return loop.has_tg_token ? 'telegram' : ''
}

// Whether this hub has the Slack surface at all. A hub from before #230
// sends no `surface`, and its PATCH ignores fields it does not know: it
// would answer 200 to a token pair and store nothing. So the room offers the
// token fields only where the field says the surface exists.
export function hubHasSlack(loop: Pick<LoopView, 'surface'>): boolean {
  return loop.surface !== undefined
}

// Whether the pair is worth sending. Both fields or nothing: the PATCH reads
// both empty as *detach*, and one alone is a 400, so the button stays off
// until both hold something. Whitespace is empty, as for the Telegram token.
export function slackPairSubmittable(appToken: string, botToken: string): boolean {
  return appToken.trim() !== '' && botToken.trim() !== ''
}

// The codes #230's attach answers with. Each has its own words below, and a
// test holds the two lists together, so a code added here without a message
// fails rather than falling through to the generic line.
export const SLACK_ATTACH_CODES = [
  'slack_bot_token_rejected',
  'slack_app_token_rejected',
  'surface_in_use',
  'slack_workspace_mismatch',
] as const

export type SlackAttachCode = (typeof SLACK_ATTACH_CODES)[number]

// Which field an error belongs to, so the form can mark the one to fix.
export type SlackTokenField = 'app' | 'bot' | ''

export interface SlackAttachError {
  field: SlackTokenField
  message: string
}

// Slack's own reason inside the hub's sentence for a rejected token, which
// wraps it twice: "slack bot token rejected: slack auth.test: invalid_auth".
// Quoting the sentence whole after "Slack rejected the bot token" said the
// refusal twice (#437). Slack's reasons are snake_case codes, so a last
// segment that is one is the reason; anything else, a network error say,
// keeps its detail and loses only the hub's prefix.
export function slackReason(message: string): string {
  const cut = message.lastIndexOf(': ')
  const last = cut < 0 ? message : message.slice(cut + 2)
  if (/^[a-z][a-z0-9_]*$/.test(last)) return last
  return message.replace(/^slack (bot|app-level) token rejected: /, '')
}

// What a failed attach says. For the two rejections it quotes Slack's own
// reason; the other two are the hub's rules and are said in full, with what
// to do about them.
export function slackAttachError(e: unknown): SlackAttachError {
  const message = e instanceof Error ? e.message : String(e)
  const code = e instanceof ApiError ? e.code : ''
  const reason = slackReason(message)
  switch (code as SlackAttachCode) {
    case 'slack_bot_token_rejected':
      return { field: 'bot', message: `Slack rejected the bot token: ${reason}` }
    // Only a missing scope is the scope's fault. invalid_auth is as often a
    // bot token pasted into this field, so the rest get a check, not a cause.
    case 'slack_app_token_rejected':
      return {
        field: 'app',
        message: /missing_scope/.test(reason)
          ? `Slack rejected the app-level token: ${reason}. Generate one with the connections:write scope.`
          : `Slack rejected the app-level token: ${reason}. Check that it is the app-level token, xapp-…, and not the bot token.`,
      }
    case 'surface_in_use':
      return {
        field: '',
        message: 'This loop already has a Telegram bot, and a loop has one surface. Detach Telegram first.',
      }
    case 'slack_workspace_mismatch':
      return {
        field: '',
        message:
          "This app is in a different workspace from this hub's other Slack loops. A hub's Slack loops share one workspace, because the fleet channel is one channel. Create the app in that workspace instead.",
      }
  }
  return { field: '', message: message || 'could not save the tokens' }
}

// Which list an `access` frame changed. Telegram's frames predate the field,
// so a frame without one is Telegram's.
export function accessSurface(frame: Record<string, unknown>): 'telegram' | 'slack' {
  return frame.surface === 'slack' ? 'slack' : 'telegram'
}

// A Slack sender as a person picks them: the handle first, the display name
// behind it, the user id when there is neither.
export function slackSenderLabel(s: Pick<SlackSender, 'slack_user_id' | 'username' | 'display'>): string {
  if (!s.username) return s.display || s.slack_user_id
  return s.display ? `@${s.username} · ${s.display}` : `@${s.username}`
}

// Whether a Slack loop can message its owner, in words. Slack lets the bot
// open the DM itself, so unlike Telegram nothing waits on the owner writing
// first: an owner and a connected app are the whole of it.
export function slackReadiness(
  loop: Pick<LoopView, 'owner_slack_user_id' | 'owner_username' | 'owner_dm_ready'>,
  status?: Pick<SlackStatus, 'bridge'>,
): string {
  if (!loop.owner_slack_user_id) return 'No owner set, so this loop cannot message anyone privately.'
  const owner = loop.owner_username || loop.owner_slack_user_id
  if (loop.owner_dm_ready) return `Ready: the loop can message ${owner} privately.`
  if (status && !status.bridge.connected)
    return `Waiting for the Slack app to connect before it can message ${owner}.`
  return `Not ready to message ${owner} yet.`
}

// The Slack channel the loop's fleet room is, or that it has none yet; the
// loop hears every room it has bound (#548), which the rooms panel lists.
export function slackChannel(status: Pick<SlackStatus, 'channel_id' | 'channel_name'>): string {
  if (!status.channel_id) return 'not bound yet'
  return status.channel_name ? `#${status.channel_name}` : status.channel_id
}

// What binds it. An invite alone does not: the fleet channel binds to the
// first Slack channel an allowed sender writes in, and a pending sender's
// message is dropped before that (#230), so an operator told to invite the
// app would re-invite one that is already there (#438).
export const SLACK_UNBOUND_HINT =
  'The fleet channel binds to the first Slack channel an allowed sender writes in with the app invited. A pending sender does not count: allow them on Access.'

// An app created from a manifest older than ADR-0040 lacks the reaction
// scopes and events. Slack keeps the app as created, so nothing here can add
// them: the operator updates the manifest and reinstalls once (#535).
export const SLACK_REACTIONS_HINT =
  'Reactions need the reactions:read and reactions:write scopes and the reaction_added and reaction_removed events. An app created before them gets them under OAuth & Permissions and Event Subscriptions in its Slack settings, then a reinstall; until then reactions stay on the hub.'

// An app created from a manifest older than ADR-0041 has interactivity off,
// so a click on a poll's buttons never reaches it. Slack keeps the app as
// created: the operator switches it on once (#552, #554).
export const SLACK_POLLS_HINT =
  'Polls need interactivity. An app created before polls gets it under Interactivity & Shortcuts in its Slack settings; until then its polls show, and clicks on them do nothing.'
