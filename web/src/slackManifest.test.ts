import { describe, it, expect } from 'vitest'
import { slackCreateAppURL, slackManifest } from './slackManifest'

describe('slackManifest', () => {
  // The whole shape, pinned: every key is one Slack reads, and a change here
  // changes what an operator's app can do.
  it('is a Socket Mode app named after the loop', () => {
    expect(slackManifest('aster')).toEqual({
      display_information: { name: 'aster', description: 'The Spool loop aster.' },
      features: {
        app_home: { messages_tab_enabled: true, messages_tab_read_only_enabled: false },
        bot_user: { display_name: 'aster', always_online: false },
      },
      oauth_config: {
        scopes: {
          bot: [
            'chat:write',
            'im:history',
            'im:write',
            'channels:history',
            'groups:history',
            'channels:read',
            'groups:read',
            'users:read',
          ],
        },
      },
      settings: {
        event_subscriptions: { bot_events: ['message.im', 'message.channels', 'message.groups'] },
        interactivity: { is_enabled: false },
        org_deploy_enabled: false,
        socket_mode_enabled: true,
        token_rotation_enabled: false,
      },
    })
  })

  // Slack caps an app name at 35 characters and a bot's display name to
  // a-z, 0-9, -, _ and . (its manifest reference); a loop name is at most 32
  // of a-z, 0-9, - and _.
  it('fits the longest loop name Slack will take', () => {
    const m = slackManifest('a0_-'.repeat(8))
    expect(m.display_information.name.length).toBeLessThanOrEqual(35)
    expect(m.features.bot_user.display_name).toMatch(/^[a-z0-9._-]+$/)
  })
})

describe('slackCreateAppURL', () => {
  it('carries the manifest to the create page intact', () => {
    const manifest = slackManifest('aster')
    const url = new URL(slackCreateAppURL(manifest))
    expect(url.origin + url.pathname).toBe('https://api.slack.com/apps')
    expect(url.searchParams.get('new_app')).toBe('1')
    expect(JSON.parse(url.searchParams.get('manifest_json') ?? '')).toEqual(manifest)
  })
})
