// A browser walk of the control room, served by the real binary against a
// fixture store: the smoke CI runs on a web change (#356).
//
// CI's other web gates read the source: tsc, lint, format, vitest, the build.
// None of them opens the built room, so a page that renders blank, a route
// the shipped binary does not serve, or a stream that never connects passed
// green. The audit of 2026-09-25 found a loop page stuck on "Loading…" by hand
// (#349). This walk is where that is found instead.
//
// Every check is a text or role query, never pixels. Run by `make ui-smoke`,
// or in CI, through scripts/fixture-hub.sh, which provides SPOOL_URL and
// SPOOL_DATA_DIR. On a failure the Playwright trace is written to
// SPOOL_SMOKE_TRACE (default smoke-trace.zip), so the reason is readable
// without a rerun: `npx playwright show-trace <file>`.
//
// The trace records the fixture hub's operator token, as the login form and
// the session cookie carry it. That token belongs to a hub on a temp
// directory that is deleted when the walk ends, so it opens nothing.
import { chromium } from 'playwright'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const base = process.env.SPOOL_URL
const dataDir = process.env.SPOOL_DATA_DIR
if (!base || !dataDir) {
  console.error('smoke: SPOOL_URL and SPOOL_DATA_DIR are required; run it through scripts/fixture-hub.sh')
  process.exit(2)
}
const tracePath = resolve(process.env.SPOOL_SMOKE_TRACE ?? 'smoke-trace.zip')
// Read, never printed.
const token = readFileSync(`${dataDir}/operator-token`, 'utf8').trim()

// Long enough for a cold CI runner, short enough that a hang fails the step
// well inside its budget.
const WAIT = 15000

const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: 1180, height: 900 } })
await ctx.tracing.start({ screenshots: true, snapshots: true })
const page = await ctx.newPage()

// An exception thrown in the page is a failure wherever the walk is, even when
// the text it looks for still rendered.
const pageErrors = []
page.on('pageerror', (err) => pageErrors.push(err.message))

let step = ''
async function see(locator, what) {
  try {
    await locator.first().waitFor({ state: 'visible', timeout: WAIT })
  } catch {
    throw new Error(`${step}: ${what} never appeared`)
  }
}
async function gone(locator, what) {
  try {
    await locator.first().waitFor({ state: 'detached', timeout: WAIT })
  } catch {
    throw new Error(`${step}: ${what} was still showing after ${WAIT / 1000}s`)
  }
}
async function heading(name) {
  await see(page.getByRole('heading', { level: 1, name, exact: true }), `the ${name} heading`)
}

try {
  step = 'login'
  await page.goto(base + '/')
  await see(page.locator('#operator-token'), 'the operator-token field')
  // The room opens its global stream once the session is in, so listen before
  // signing in.
  const stream = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/stream', { timeout: WAIT })
  await page.fill('#operator-token', token)
  await page.click('button[type=submit]')
  await see(page.getByRole('navigation').getByRole('link', { name: 'Fleet' }), 'the Fleet destination')

  step = 'stream'
  const res = await stream.catch(() => {
    throw new Error(`${step}: the room never requested /api/stream`)
  })
  const type = res.headers()['content-type'] ?? ''
  if (res.status() !== 200 || !type.startsWith('text/event-stream')) {
    throw new Error(`${step}: /api/stream answered ${res.status()} ${type}`)
  }

  step = 'Fleet'
  await heading('Fleet')
  for (const name of ['gardener', 'watcher', 'courier', 'archivist']) {
    await see(page.locator('.fleet-row', { hasText: name }), `the ${name} row`)
  }

  step = 'Fleet channel'
  await page.getByRole('link', { name: 'Fleet channel' }).click()
  await see(page.getByText('Three pages, all fixed', { exact: false }), "gardener's channel post")

  step = 'loop page'
  await page.goto(base + '/loops/gardener')
  // The heading carries the loop's state after its name.
  await see(page.getByRole('heading', { level: 1, name: /^@gardener\b/ }), 'the @gardener heading')
  await see(page.getByText('Three pages referred to', { exact: false }), "the timeline's assistant text")
  await see(page.getByRole('combobox', { name: 'model' }), 'the model control')
  await gone(page.getByText('Loading…', { exact: true }), '"Loading…"')

  step = 'Rules'
  await page.goto(base + '/rules')
  await heading('Fleet rules')

  step = 'Settings'
  await page.goto(base + '/settings')
  await heading('Settings')
  await see(page.getByText(/Configured|Not configured/), 'the Claude token state')

  step = 'Access'
  await page.goto(base + '/access')
  await heading('Access')
  await see(page.getByText('Rana', { exact: false }), 'the fixture sender')
  await gone(page.getByText('Loading senders…'), '"Loading senders…"')

  // Reachable, and nothing more: the page is the operator's own conversations,
  // so the walk asserts its heading and reads none of it.
  step = 'Activity'
  await page.goto(base + '/activity')
  await heading('Activity')

  step = 'the page'
  if (pageErrors.length > 0) throw new Error(`${step} threw: ${pageErrors.join('; ')}`)

  await ctx.tracing.stop()
  console.log('smoke: the control room walked clean')
} catch (err) {
  await ctx.tracing.stop({ path: tracePath })
  console.error(`smoke: ${err.message}`)
  console.error(`smoke: trace at ${tracePath}`)
  process.exitCode = 1
} finally {
  await browser.close()
}
