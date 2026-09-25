// The Slack app a loop runs as, as a manifest the operator creates it from
// (#345). One app per loop and Socket Mode, so no request URL: a hub on a
// laptop has no public ingress, which is the same reason Telegram is
// long-polled (#230's decisions of 2026-09-22).
//
// The scopes and events are the Slack surface's contract, not a UI choice:
// they are the list the surface (#230) is built against, and its tier-2
// fixture asserts the same one. Change them there first.

// What the bot may call, each for a path the surface takes (Terra's list on
// #345). `auth.test`, which checks a pasted token, needs none.
export const slackBotScopes = [
  // posting in the group and the owner DM; a thread reply is the same call
  'chat:write',
  // the owner DM: hearing it, and opening it on a tick before the owner has
  // written
  'im:history',
  'im:write',
  // the group, public or private: hearing it, and `conversations.info` on it
  // so the surface can say "not a member" instead of going quiet. A team's
  // working channel is often private, and without the `groups:` pair the
  // attach would fail with nothing to say why.
  'channels:history',
  'groups:history',
  'channels:read',
  'groups:read',
  // who a sender is, for the envelope and the pairing prompt
  'users:read',
] as const

// What Slack pushes over the socket. No `app_mention`: the channel events
// already carry every message in the group, mentions included, and the
// router finds mentions in the text, so it would deliver each one twice.
export const slackBotEvents = ['message.im', 'message.channels', 'message.groups'] as const

export interface SlackManifest {
  display_information: { name: string; description: string }
  features: {
    // The Messages tab, writable, is how the owner DMs the bot at all:
    // without it there is no owner DM.
    app_home: { messages_tab_enabled: true; messages_tab_read_only_enabled: false }
    bot_user: { display_name: string; always_online: false }
  }
  oauth_config: { scopes: { bot: string[] } }
  settings: {
    event_subscriptions: { bot_events: string[] }
    interactivity: { is_enabled: false }
    org_deploy_enabled: false
    socket_mode_enabled: true
    token_rotation_enabled: false
  }
}

// The manifest for one loop. The loop's name is the app's name and the bot's
// display name, so the operator's Slack reads the same handle as this room.
// A loop name fits both as it is: at most 32 characters of a-z, 0-9, - and
// _, inside Slack's 35 for an app name and the characters it allows a bot's
// display name.
export function slackManifest(loopName: string): SlackManifest {
  return {
    display_information: {
      name: loopName,
      description: `The Spool loop ${loopName}.`,
    },
    features: {
      app_home: { messages_tab_enabled: true, messages_tab_read_only_enabled: false },
      bot_user: { display_name: loopName, always_online: false },
    },
    oauth_config: { scopes: { bot: [...slackBotScopes] } },
    settings: {
      event_subscriptions: { bot_events: [...slackBotEvents] },
      interactivity: { is_enabled: false },
      org_deploy_enabled: false,
      socket_mode_enabled: true,
      // Rotation would expire the bot token every twelve hours, and the
      // surface stores the one the operator pasted.
      token_rotation_enabled: false,
    },
  }
}

// Slack's create-app page, opened with the manifest already in it. The
// operator still picks the workspace and confirms; nothing is created by
// following the link.
export function slackCreateAppURL(manifest: SlackManifest): string {
  return `https://api.slack.com/apps?new_app=1&manifest_json=${encodeURIComponent(JSON.stringify(manifest))}`
}
