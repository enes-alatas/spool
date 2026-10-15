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
// The trace records the fixture user's password, as the login form carries
// it, and the session cookie. cmd/uifixture seeds that password, and the
// hub that issued the cookie is gone, so a public trace holds no credential.
import { chromium } from 'playwright'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'

const base = process.env.SPOOL_URL
const dataDir = process.env.SPOOL_DATA_DIR
if (!base || !dataDir) {
  console.error('smoke: SPOOL_URL and SPOOL_DATA_DIR are required; run it through scripts/fixture-hub.sh')
  process.exit(2)
}
const tracePath = resolve(process.env.SPOOL_SMOKE_TRACE ?? 'smoke-trace.zip')
// Read, never printed: the password the fixture gave its users (#674).
const password = readFileSync(`${dataDir}/fixture-user-password`, 'utf8').trim()

// Long enough for a cold CI runner, short enough that a hang fails the step
// well inside its budget.
const WAIT = 15000

const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: 1180, height: 900 } })
// the copy chips write the clipboard, and the walk reads it back
await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base })
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
  await see(page.locator('#username'), 'the username field')
  // The room opens its global stream once the session is in, so listen before
  // signing in.
  const stream = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/stream', { timeout: WAIT })
  await page.fill('#username', 'admin')
  await page.fill('#password', password)
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

  // The page opens at the tail of the timeline, also when the loop record
  // arrives after its events: the pane mounts only then, and the page used to
  // open at the top (#846). The record is held back to force that order, and
  // the window is short enough that the timeline scrolls at all, or the check
  // would pass on a pane with nothing to scroll.
  step = 'timeline tail'
  await page.setViewportSize({ width: 1180, height: 520 })
  await page.route('**/api/loops/gardener', async (route) => {
    await sleep(1500)
    await route.continue()
  })
  await page.goto(base + '/loops/gardener')
  await see(page.locator('.pane-scroll [data-entry-id]'), 'a timeline entry')
  // How far the pane sits above its tail, given a moment to get there: a
  // page that never scrolls stays put, and one that does is there at once.
  const pane = page.locator('.pane-scroll')
  const position = () =>
    pane.evaluate((el) => ({
      scrolls: el.scrollHeight > el.clientHeight,
      below: el.scrollHeight - el.scrollTop - el.clientHeight,
    }))
  let tail = await position()
  for (let tries = 0; tail.below >= 32 && tries < 20; tries++) {
    await sleep(250)
    tail = await position()
  }
  if (!tail.scrolls) throw new Error(`${step}: the timeline fits its pane, so the check proves nothing`)
  if (tail.below >= 32)
    throw new Error(`${step}: the timeline opened ${Math.round(tail.below)}px above its tail`)
  await page.unroute('**/api/loops/gardener')
  await page.setViewportSize({ width: 1180, height: 900 })

  // The side column scrolls on its own (#848): its last card brought into
  // view moves the column, not the page, so the timeline stays on screen.
  // A column that fits the window would pass without proving anything.
  step = 'side column'
  const aside = page.locator('.detail > aside')
  await see(aside.locator('.side-panel'), 'a side card')
  const column = await aside.evaluate((el) => {
    const main = el.ownerDocument.querySelector('.main')
    const taller = el.scrollHeight > main.clientHeight
    el.querySelector('.side-panel:last-of-type').scrollIntoView({ block: 'end' })
    return { taller, column: el.scrollTop, page: main.scrollTop }
  })
  if (!column.taller) throw new Error(`${step}: the column fits the window, so the check proves nothing`)
  if (column.column === 0 || column.page !== 0) {
    throw new Error(
      `${step}: the last card scrolled the page ${column.page}px and the column ${column.column}px`,
    )
  }
  // And the way back up: the button the scroll brought, which takes the
  // column to its top and goes away there.
  await page.getByRole('button', { name: 'Back to the top' }).click()
  await gone(page.getByRole('button', { name: 'Back to the top' }), 'the back-to-top button')

  // The controls are round icons (#858), each named on hover: the label for
  // the rightmost must show whole inside the column, which clips whatever
  // overflows it, and a screen reader gets the same line as a description.
  step = 'controls'
  const del = aside.getByRole('button', { name: 'Delete loop' })
  await del.hover()
  const tip = del.locator('.ctl-tip')
  await see(tip, 'the Delete loop label')
  const [tipBox, asideBox] = [await tip.boundingBox(), await aside.boundingBox()]
  if (tipBox.x < asideBox.x || tipBox.x + tipBox.width > asideBox.x + asideBox.width) {
    throw new Error(`${step}: the Delete loop label runs past the column, where it is clipped`)
  }
  const described = await del.evaluate((el) =>
    el.ownerDocument.getElementById(el.getAttribute('aria-describedby') ?? '')?.textContent.trim(),
  )
  if (!described) throw new Error(`${step}: Delete loop has no description for a screen reader`)
  await page.mouse.move(0, 0)

  // The mission's editor holds an edit, so a stray dismissal must not throw
  // it away (#847): Escape, twice, as a reflex does. Chromium lets a page
  // refuse only the first Escape after the reader's last input, so the
  // second is the one that tests the dialog.
  step = 'mission dialog'
  await page.locator('.side-panel').getByRole('button', { name: 'Edit the mission' }).click()
  const missionDialog = page.locator('dialog.mission-dialog')
  const missionText = missionDialog.getByRole('textbox', { name: 'Mission' })
  await see(missionText, 'the mission editor')
  // Typed, not filled: the keys are the reader's input that lets the page
  // refuse the first Escape, and the second is then the browser's to take.
  await missionText.click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type(' Draft.')
  if (!(await missionText.inputValue()).endsWith(' Draft.')) {
    throw new Error(`${step}: typing never reached the mission editor`)
  }
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await sleep(200)
  if (!(await missionDialog.evaluate((el) => el.open).catch(() => false))) {
    throw new Error(`${step}: Escape closed the dialog and lost the edit`)
  }
  if (!(await missionText.inputValue()).endsWith(' Draft.')) {
    throw new Error(
      `${step}: the edit did not survive Escape: ${JSON.stringify((await missionText.inputValue()).slice(-40))}`,
    )
  }
  // A save in flight holds the dialog, the ✕ included, so a save that fails
  // says so in it: closed, it would say so nowhere. The hub is made to take
  // its time and then refuse.
  await page.route('**/api/loops/gardener', async (route) => {
    if (route.request().method() !== 'PATCH') return route.continue()
    await sleep(1000)
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: '{"error":"refused for the smoke"}',
    })
  })
  await missionDialog.getByRole('button', { name: 'Save' }).click()
  if (!(await missionDialog.getByRole('button', { name: 'Close' }).isDisabled())) {
    throw new Error(`${step}: the ✕ could close the dialog during a save`)
  }
  await see(missionDialog.getByRole('alert'), "the failed save's error")
  await page.unroute('**/api/loops/gardener')
  await missionDialog.getByRole('button', { name: 'Cancel' }).click()
  await gone(missionDialog, 'the mission dialog')

  step = 'Rules'
  await page.goto(base + '/rules')
  await heading('Fleet rules')

  step = 'Settings'
  await page.goto(base + '/settings')
  await heading('Settings')
  await see(page.getByText(/Configured|Not configured/), 'the Claude token state')

  // A literal to type copies on a click (#870): the clipboard holds the
  // command afterwards, and the chip says so.
  step = 'copy chip'
  const chip = page.getByRole('button', { name: 'Copy claude setup-token' }).first()
  await chip.click()
  await see(page.locator('.copyable.copied'), 'the copied chip')
  const copied = await page.evaluate(() => globalThis.navigator.clipboard.readText())
  if (copied !== 'claude setup-token')
    throw new Error(`${step}: the clipboard holds ${JSON.stringify(copied)}`)
  // Over plain http on a LAN address there is no clipboard API, so the copy
  // goes through execCommand (#870): with writeText refused, the clipboard
  // still holds the command. Where execCommand refuses too, the click
  // selects the command and says to press Ctrl+C.
  await sleep(1600)
  await page.evaluate(async () => {
    const clipboard = globalThis.navigator.clipboard
    await clipboard.writeText('')
    clipboard.writeText = () => Promise.reject(new Error('no clipboard here'))
  })
  // Copied from the keyboard, so a lost focus would show: the textarea the
  // copy selects must hand focus back to the chip.
  await chip.focus()
  await page.keyboard.press('Enter')
  await see(page.locator('.copyable.copied'), 'the chip copied without the clipboard API')
  if (!(await chip.evaluate((el) => el === globalThis.document.activeElement))) {
    throw new Error(`${step}: copying without the clipboard API took focus off the chip`)
  }
  const byCommand = await page.evaluate(() => globalThis.navigator.clipboard.readText())
  if (byCommand !== 'claude setup-token') {
    throw new Error(`${step}: without the clipboard API the clipboard holds ${JSON.stringify(byCommand)}`)
  }
  await sleep(1600)
  await page.evaluate(() => {
    globalThis.document.execCommand = () => false
  })
  await chip.click()
  const title = await chip.getAttribute('title')
  const selected = await page.evaluate(() => String(globalThis.getSelection()))
  if (title !== 'Selected: press Ctrl+C' || selected !== 'claude setup-token') {
    throw new Error(
      `${step}: with no way to copy, the chip said ${JSON.stringify(title)} and selected ${JSON.stringify(selected)}`,
    )
  }

  // Access is a part of Settings since #732; the old address lands there.
  step = 'Access'
  await page.goto(base + '/access')
  await heading('Settings')
  await see(page.getByRole('heading', { name: 'Users and access' }), 'the Users and access section')
  await see(page.getByText('@rana_example', { exact: false }), 'the sender not linked to a user')

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
