// The loops' change record (#695, #696): what each change says on the
// loop's own page, which names the loop already.

// What each action did, as the loop's page lists its changes.
const SAID: Record<string, string> = {
  create: 'created',
  edit: 'edited',
  delete: 'deleted',
  pause: 'paused',
  resume: 'resumed',
  wake: 'woken',
  kill: 'process killed',
  rotate: 'session rotated',
  rehome: 'rehomed',
  cloud_token: 'hub token minted',
  cloud_allowlist: 'allowed domains confirmed',
  cloud_check: 'cloud setup checked',
  workstation_restart: 'workstation restarted',
  workstation_poweroff: 'workstation powered off',
  workstation_poweron: 'workstation powered on',
  workstation_recreate: 'workstation recreated',
  owner: 'owner changed',
  room: 'room bound',
  unroom: 'room unbound',
  attach: 'connection attached',
  detach: 'connection detached',
  join: 'added to a channel',
  leave: 'taken out of a channel',
  acknowledge: 'problem acknowledged',
}

// "paused", on the loop's own page, where the row goes on to say by whom.
// An action this build does not know is still a real change, and is said by
// its name.
export function changeOnPage(action: string): string {
  return SAID[action] ?? action
}
