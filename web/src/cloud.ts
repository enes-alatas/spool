// Settings → Cloud (ADR-0056, #865): what the hub's cloud setup says, step
// by step. The hub checks each step; these read its answer.

import type { CloudCheck, CloudCheckStage, CloudSettings } from './api'

// The port the cloud listener serves on, from its address: what a tunnel
// forwards to. '' while the listener is off.
export function listenerPort(addr: string | undefined): string {
  if (!addr) return ''
  const colon = addr.lastIndexOf(':')
  return colon < 0 ? '' : addr.slice(colon + 1)
}

export type Tone = 'ok' | 'bad' | 'todo'

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
}

// The check as rows: every stage before the one it stopped at passed, that
// one failed, and the rest were never reached. `refused` stops before any
// stage, so it is a row of its own.
export function checkRows(check: CloudCheck | undefined): CheckRow[] {
  if (!check) return []
  if (check.state === 'ok') return ROWS.map(({ key, text, failed }) => ({ key, text, failed, tone: 'ok' }))
  const stage = check.stage
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
  }))
}

// The address step's state on the status page, short.
export function addressState(view: CloudSettings): { text: string; tone: Tone } {
  if (!view.address) return { text: 'not set', tone: 'todo' }
  if (!view.check) return { text: 'not checked yet', tone: 'todo' }
  if (view.check.state === 'ok') return { text: 'reaches this hub', tone: 'ok' }
  const failing = checkRows(view.check).find((row) => row.tone === 'bad')
  return { text: `fails: ${failing?.failed ?? 'unknown'}`, tone: 'bad' }
}

// The step the guided setup opens on: the first one not done.
export type SetupStep = 'listener' | 'address' | 'done'

export function firstOpenStep(view: CloudSettings): SetupStep {
  if (!view.listener.on) return 'listener'
  if (view.check?.state !== 'ok') return 'address'
  return 'done'
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
