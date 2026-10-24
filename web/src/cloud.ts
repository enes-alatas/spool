// Settings → Cloud (ADR-0056, #865): what the hub's cloud setup says, step
// by step. The hub checks each step; these read its answer.

import type {
  CloudAccountStage,
  CloudCheck,
  CloudCheckStage,
  CloudEnvironmentChoice,
  CloudEnvironmentView,
  CloudSettings,
} from './api'

// The port the cloud listener serves on, from its address: what a tunnel
// forwards to. '' while the listener is off.
export function listenerPort(addr: string | undefined): string {
  if (!addr) return ''
  const colon = addr.lastIndexOf(':')
  return colon < 0 ? '' : addr.slice(colon + 1)
}

export type Tone = 'ok' | 'warn' | 'bad' | 'todo'

// One stage of the address check, in the order a request meets them: the
// listener has to be on, the name has to resolve, the address has to take a
// connection, its certificate has to be good, and this hub has to answer.
export interface CheckRow {
  key: string
  text: string
  tone: Tone
  // The row said as a failure, short, for the step's line.
  failed: string
  // Where the check stopped: what to try. Only on the failing row.
  hint?: string
  // A page that says more about the hint, when there is one.
  docs?: string
}

const ROWS: { key: string; text: string; failed: string; stages: CloudCheckStage[] }[] = [
  {
    key: 'listener',
    text: 'the cloud listener is on',
    failed: 'the listener is off',
    stages: ['listener_off'],
  },
  { key: 'dns', text: 'the name resolves', failed: 'the name does not resolve', stages: ['dns'] },
  {
    key: 'connect',
    text: 'the address takes a connection',
    failed: 'no connection',
    stages: ['unreachable', 'timeout'],
  },
  {
    key: 'tls',
    text: 'its HTTPS certificate is valid',
    failed: 'the certificate is not valid',
    stages: ['tls'],
  },
  { key: 'answer', text: 'this hub answered', failed: 'another server answered', stages: ['other_server'] },
]

// What each failing stage asks of the operator.
const HINT: Record<CloudCheckStage, string> = {
  listener_off: 'Turn the cloud listener on: nothing on this hub can answer while it is off.',
  refused: 'This hub no longer accepts the stored address, so it made no request. Enter an HTTPS address.',
  dns: 'The name does not resolve. If the tunnel was just created, its DNS record may take a minute.',
  unreachable: 'Nothing took the connection. Is the tunnel or proxy running?',
  timeout: 'Nothing answered in time. Is the tunnel or proxy running, and pointed at this machine?',
  tls: 'The certificate is not valid for this name. A tunnel serves one for you; a proxy needs its own.',
  other_server:
    'Something answered, but not this hub. Point the tunnel or proxy at the cloud listener’s port.',
  session:
    'Make the address reachable from the internet, then press Check again. On Tailscale, run tailscale funnel status to see whether the Funnel is on.',
}

// The two lines no stage of the hub's own request can say (#1093): it
// passed over a private route, so the internet is untested, or a cloud
// session's start never arrived though it passed. Each is a row after
// the five, since all five passed from this machine.
const SESSION_ROW = {
  key: 'session',
  text: 'a cloud session couldn’t reach this hub at this address, though this machine can',
  failed: 'a cloud session can’t reach it',
}
// A CheckRow needs a `failed`, but this one is never read: the row comes
// only on a pass, and a passing step's line doesn't ask the rows.
const PRIVATE_ROW = {
  key: 'private',
  text: 'reached over a private route from this machine',
  failed: 'reached over a private route only',
}

// The outside test for an address the hub reached privately: from off the
// tailnet or LAN, the listener answers /mcp with this hub's 401.
export function privateRouteHint(address: string): string {
  const mcp = address ? `${address.replace(/\/+$/, '')}/mcp` : 'https://<address>/mcp'
  return `This machine reached the address over a private route, such as a tailnet, so the check doesn’t show the internet can. From a machine off that network, curl -i ${mcp} should get this hub’s 401.`
}

// A Tailscale Funnel's certificate fails the check only from this machine
// when a proxy here holds :443 (#963): the machine reaches its own ts.net
// name on its tailnet address, where the proxy answers instead.
const TAILSCALE_TLS_HINT =
  'The certificate is not valid for this name from this machine. If a proxy such as Caddy holds :443 here, it answers this machine’s requests for its ts.net name: give the proxy that name with Tailscale’s certificate.'
export const FUNNEL_BESIDE_PROXY_DOCS =
  'https://github.com/enes-alatas/spool/blob/main/docs/OPERATIONS.md#a-tailscale-funnel-beside-caddy'

// The check as rows: every stage before the one it stopped at passed, that
// one failed, and the rest were never reached. `refused` stops before any
// stage, so it is a row of its own.
export function checkRows(check: CloudCheck | undefined, address = ''): CheckRow[] {
  if (!check) return []
  const passed: CheckRow[] = ROWS.map(({ key, text, failed }) => ({ key, text, failed, tone: 'ok' }))
  if (check.state === 'ok')
    return check.route === 'private'
      ? [...passed, { ...PRIVATE_ROW, tone: 'warn', hint: privateRouteHint(address) }]
      : passed
  const stage = check.stage
  if (stage === 'session') return [...passed, { ...SESSION_ROW, tone: 'bad', hint: HINT.session }]
  if (stage === 'refused')
    return [
      {
        key: 'refused',
        text: 'the address is one this hub accepts',
        failed: 'the address is not accepted',
        tone: 'bad',
        hint: HINT.refused,
      },
    ]
  const at = ROWS.findIndex((row) => stage !== undefined && row.stages.includes(stage))
  // A stage this build does not know: say it failed, by its name.
  if (at < 0)
    return [
      {
        key: 'unknown',
        text: `the check failed (${stage ?? 'no stage'})`,
        failed: stage ?? 'unknown',
        tone: 'bad',
      },
    ]
  return ROWS.map((row, i) => ({
    key: row.key,
    text: row.text,
    failed: row.failed,
    tone: i < at ? 'ok' : i === at ? 'bad' : 'todo',
    hint: i === at && stage ? HINT[stage] : undefined,
    ...(i === at && stage === 'tls' && isTailscaleAddress(address)
      ? { hint: TAILSCALE_TLS_HINT, docs: FUNNEL_BESIDE_PROXY_DOCS }
      : {}),
  }))
}

// The address step's state on the status page, short.
export function addressState(view: CloudSettings): { text: string; tone: Tone } {
  if (!view.address) return { text: 'not set', tone: 'todo' }
  if (!view.check) return { text: 'not checked yet', tone: 'todo' }
  if (view.check.state === 'ok')
    return view.check.route === 'private'
      ? { text: 'reaches this hub over a private route', tone: 'warn' }
      : { text: 'reaches this hub', tone: 'ok' }
  const failing = checkRows(view.check).find((row) => row.tone === 'bad')
  return { text: `fails: ${failing?.failed ?? 'unknown'}`, tone: 'bad' }
}

// What a failed account check says, short, and what it asks of the
// operator.
const ACCOUNT_FAILED: Record<CloudAccountStage, { failed: string; hint: string }> = {
  no_account: {
    failed: 'not signed in',
    hint: "Sign in with Claude: every loop runs on the hub's Claude login.",
  },
  refused: {
    failed: 'the login was refused',
    hint: 'Anthropic no longer accepts this login, so it may have been revoked. Sign in again.',
  },
  unreachable: {
    failed: "Anthropic's API did not answer",
    hint: "Anthropic's API did not answer. Check again in a minute.",
  },
  other: {
    failed: "Anthropic's API answered unexpectedly",
    hint: "Anthropic's API answered, but not as expected. Check again, or sign in again if it keeps failing.",
  },
}

// The account step's state on the status page, short, with what to try when
// its check failed (ADR-0058: the hub's one Claude login). A login whose
// check fails doesn't hold cloud loops, but nothing runs on it while
// Anthropic refuses it.
export function accountState(view: CloudSettings): { text: string; tone: Tone; hint?: string } {
  const { set, check } = view.account
  if (!set) return { text: 'not signed in', tone: 'todo' }
  if (!check) return { text: 'signed in, not checked yet', tone: 'todo' }
  if (check.state === 'ok') return { text: 'signed in', tone: 'ok' }
  const failed = check.stage ? ACCOUNT_FAILED[check.stage] : undefined
  return {
    text: `fails: ${failed?.failed ?? check.stage ?? 'unknown'}`,
    tone: 'bad',
    hint: failed?.hint,
  }
}

// Settings → Claude login's line (ADR-0058): the account step's state,
// except on a hub that can't sign in because it has no claude CLI to run,
// neither the workstation image's nor this machine's (#952), which says that
// rather than "not signed in" with no way to sign in. A login signed in
// before the CLI went away still reads as itself.
export function loginState(view: CloudSettings): { text: string; tone: Tone; hint?: string } {
  if (!view.account.set && !view.account.cli) return { text: "can't sign in from this machine", tone: 'todo' }
  return accountState(view)
}

// The step the guided setup opens on: the first one not done.
export type SetupStep = 'listener' | 'address' | 'account' | 'environment' | 'done'

export function firstOpenStep(view: CloudSettings): SetupStep {
  if (!view.listener.on) return 'listener'
  if (view.check?.state !== 'ok') return 'address'
  if (view.account.check?.state !== 'ok') return 'account'
  if (!view.environment.id || !view.environment.confirmed) return 'environment'
  return 'done'
}

// The environment step's state on the status page (ADR-0059). An ID
// without a confirmed list still runs loops, limited to what the
// environment allows, so it warns rather than fails.
export function environmentState(view: CloudSettings): { text: string; tone: Tone } {
  const { id, confirmed } = view.environment
  if (!id) return { text: 'no environment', tone: 'todo' }
  if (confirmed) return { text: 'allowlist pasted', tone: 'ok' }
  return { text: 'allowlist not confirmed', tone: 'warn' }
}

// The environment picker's options: the account's list, led by the stored
// choice when the list doesn't show it (an ID pasted while the list failed,
// or an environment since deleted on claude.ai), so the select never shows
// another environment as chosen; and a prompt while none is chosen.
export function environmentOptions(
  choices: CloudEnvironmentChoice[],
  chosen: Pick<CloudEnvironmentView, 'id' | 'name'>,
): { value: string; label: string }[] {
  const listed = choices.map((choice) => ({ value: choice.id, label: choice.name }))
  if (!chosen.id) return [{ value: '', label: 'Pick an environment' }, ...listed]
  if (choices.some((choice) => choice.id === chosen.id)) return listed
  return [{ value: chosen.id, label: `${chosen.name || chosen.id} (not on this account's list)` }, ...listed]
}

// Whether an address's host is a Cloudflare quick tunnel's, which is handed
// to someone else once the tunnel stops. The hub says so as `warning` once
// the address is saved; the field says it as it is typed.
export function isQuickTunnel(address: string): boolean {
  try {
    return isQuickTunnelHost(new URL(address.trim()).hostname)
  } catch {
    return false
  }
}

export function isQuickTunnelHost(host: string): boolean {
  return host === 'trycloudflare.com' || host.endsWith('.trycloudflare.com')
}

// Whether an address is a Tailscale Funnel's, by its ts.net host.
export function isTailscaleAddress(address: string): boolean {
  try {
    const host = new URL(address.trim()).hostname
    return host.endsWith('.ts.net')
  } catch {
    return false
  }
}

// Whether a pasted code, its edges trimmed, has the shape the hub takes: one
// word of at most 1 KiB with no control characters (#864). The hub refuses
// any other with the same `cloud_sign_in` 400 a refused code gets, but
// before the sign-in, which goes on waiting; the page asks first, so a 400
// it does get always means the sign-in has ended.
export function signInCodeShaped(code: string): boolean {
  const trimmed = code.trim()
  return trimmed !== '' && new TextEncoder().encode(trimmed).length <= 1024 && !/[\s\p{Cc}]/u.test(trimmed)
}
