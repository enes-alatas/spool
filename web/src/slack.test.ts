import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import {
  accessSurface,
  hubHasSlack,
  loopSurface,
  SLACK_ATTACH_CODES,
  SLACK_UNBOUND_HINT,
  slackAttachError,
  slackChannel,
  slackPairSubmittable,
  slackReadiness,
  slackReason,
  slackSenderLabel,
} from './slack'

describe('loopSurface', () => {
  it('reads the field when the hub sends it', () => {
    expect(loopSurface({ surface: 'slack', has_tg_token: false })).toBe('slack')
    expect(loopSurface({ surface: '', has_tg_token: false })).toBe('')
  })

  it('falls back to the Telegram token on a hub from before the field', () => {
    expect(loopSurface({ has_tg_token: true })).toBe('telegram')
    expect(loopSurface({ has_tg_token: false })).toBe('')
  })
})

describe('hubHasSlack', () => {
  // A hub from before #230 answers 200 to a token pair and stores nothing,
  // so the form must not be offered there.
  it('is false on a hub that sends no surface field', () => {
    expect(hubHasSlack({})).toBe(false)
  })

  it('is true once the hub sends the field, whatever the loop is on', () => {
    expect(hubHasSlack({ surface: '' })).toBe(true)
    expect(hubHasSlack({ surface: 'telegram' })).toBe(true)
  })
})

describe('slackPairSubmittable', () => {
  it('wants both tokens, since both empty is a detach and one alone a 400', () => {
    expect(slackPairSubmittable('xapp-1-synthetic', 'xoxb-synthetic')).toBe(true)
    expect(slackPairSubmittable('xapp-1-synthetic', '')).toBe(false)
    expect(slackPairSubmittable('', 'xoxb-synthetic')).toBe(false)
    expect(slackPairSubmittable(' ', '\n')).toBe(false)
  })
})

describe('slackAttachError', () => {
  const generic = slackAttachError(new ApiError(400, 'bad json', '')).message

  // The bar #383 sets: every error code in #230's contract has a message of
  // its own, not the generic fallback.
  it.each(SLACK_ATTACH_CODES)('%s has its own message', (code) => {
    const got = slackAttachError(new ApiError(code.endsWith('rejected') ? 400 : 409, 'the reason', code))
    expect(got.message).not.toBe(generic)
    expect(got.message).not.toBe('the reason')
  })

  it('marks the field Slack rejected, quoting its reason', () => {
    expect(slackAttachError(new ApiError(400, 'invalid_auth', 'slack_bot_token_rejected'))).toEqual({
      field: 'bot',
      message: 'Slack rejected the bot token: invalid_auth',
    })
    const app = slackAttachError(new ApiError(400, 'missing_scope', 'slack_app_token_rejected'))
    expect(app.field).toBe('app')
    expect(app.message).toContain('missing_scope')
    expect(app.message).toContain('connections:write')
  })

  // #437: the hub's error wraps Slack's reason in its own sentence, and
  // quoting it whole said the refusal twice.
  it("quotes Slack's reason once, out of the hub's sentence", () => {
    const bot = new ApiError(
      400,
      'slack bot token rejected: slack auth.test: invalid_auth',
      'slack_bot_token_rejected',
    )
    expect(slackAttachError(bot).message).toBe('Slack rejected the bot token: invalid_auth')
    const app = new ApiError(
      400,
      'slack app-level token rejected: slack apps.connections.open: not_allowed_token_type',
      'slack_app_token_rejected',
    )
    expect(slackAttachError(app).message).toMatch(
      /^Slack rejected the app-level token: not_allowed_token_type\. /,
    )
  })

  // A transport failure reaches the hub as Go's *url.Error, with no
  // "slack <method>:" wrapper, so its last segment is prose, not a code.
  it('keeps the detail of a reason that is not a Slack code', () => {
    expect(
      slackReason(
        'slack bot token rejected: Post "https://slack.com/api/auth.test": dial tcp: lookup slack.com: no such host',
      ),
    ).toBe('Post "https://slack.com/api/auth.test": dial tcp: lookup slack.com: no such host')
    expect(slackReason('invalid_auth')).toBe('invalid_auth')
  })

  // A missing scope is one cause among several. invalid_auth is as often
  // the bot token in the wrong field, and blaming the scope would send the
  // operator to regenerate a token that was fine.
  it('blames the scope only when Slack says the scope is missing', () => {
    const wrong = slackAttachError(new ApiError(400, 'invalid_auth', 'slack_app_token_rejected'))
    expect(wrong.field).toBe('app')
    expect(wrong.message).toContain('invalid_auth')
    expect(wrong.message).not.toContain('connections:write')
    expect(wrong.message).toContain('not the bot token')
  })

  it('says what to do about the hub rules, on no field', () => {
    expect(slackAttachError(new ApiError(409, 'x', 'surface_in_use'))).toMatchObject({ field: '' })
    expect(slackAttachError(new ApiError(409, 'x', 'surface_in_use')).message).toContain('Detach Telegram')
    expect(slackAttachError(new ApiError(409, 'x', 'slack_workspace_mismatch')).message).toContain(
      'one workspace',
    )
  })

  it("keeps the server's words for anything else", () => {
    expect(slackAttachError(new ApiError(400, 'both tokens are required', ''))).toEqual({
      field: '',
      message: 'both tokens are required',
    })
    expect(slackAttachError(new TypeError('Failed to fetch')).message).toBe('Failed to fetch')
  })
})

describe('accessSurface', () => {
  it('routes a Slack frame to the Slack list and anything else to Telegram', () => {
    expect(accessSurface({ surface: 'slack' })).toBe('slack')
    expect(accessSurface({ surface: 'telegram' })).toBe('telegram')
    expect(accessSurface({})).toBe('telegram')
  })
})

describe('slackSenderLabel', () => {
  it('leads with the handle and falls back to the display name, then the id', () => {
    expect(slackSenderLabel({ slack_user_id: 'U0SYNTH01', username: 'ada', display: 'Ada L' })).toBe(
      '@ada · Ada L',
    )
    expect(slackSenderLabel({ slack_user_id: 'U0SYNTH01', username: 'ada', display: '' })).toBe('@ada')
    expect(slackSenderLabel({ slack_user_id: 'U0SYNTH01', username: '', display: 'Ada L' })).toBe('Ada L')
    expect(slackSenderLabel({ slack_user_id: 'U0SYNTH01', username: '', display: '' })).toBe('U0SYNTH01')
  })
})

describe('slackReadiness', () => {
  const connected = { bridge: { connected: true, last_event_at: 0, last_error: '', ignored_events: 0 } }
  const down = { bridge: { ...connected.bridge, connected: false } }

  it('says there is no owner', () => {
    expect(slackReadiness({ owner_dm_ready: false })).toMatch(/^No owner set/)
  })

  it('is ready once the hub says so, naming the owner', () => {
    expect(
      slackReadiness(
        { owner_slack_user_id: 'U0SYNTH01', owner_username: 'Ada L', owner_dm_ready: true },
        connected,
      ),
    ).toBe('Ready: the loop can message Ada L privately.')
  })

  it('blames the connection, not the owner, when the app is down', () => {
    expect(slackReadiness({ owner_slack_user_id: 'U0SYNTH01', owner_dm_ready: false }, down)).toBe(
      'Waiting for the Slack app to connect before it can message U0SYNTH01.',
    )
  })
})

describe('slackChannel', () => {
  it('names the bound channel, or its id before the name is known', () => {
    expect(slackChannel({ channel_id: 'C0FLEET', channel_name: 'fleet-room' })).toBe('#fleet-room')
    expect(slackChannel({ channel_id: 'C0FLEET', channel_name: '' })).toBe('C0FLEET')
  })

  // #438: an invite does not bind the channel; an allowed sender's message
  // does, and a pending sender's is dropped first.
  it('says what binds an unbound channel, not that an invite does', () => {
    expect(slackChannel({ channel_id: '', channel_name: '' })).toBe('not bound yet')
    expect(SLACK_UNBOUND_HINT).toMatch(/allowed sender/)
    expect(SLACK_UNBOUND_HINT).toMatch(/pending sender does not count/)
    expect(SLACK_UNBOUND_HINT).not.toMatch(/invite the bot/)
  })
})
