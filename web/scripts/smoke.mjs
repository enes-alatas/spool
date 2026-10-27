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

  // A private conversation opens at its newest message too (#991): the
  // follow effect didn't run on its messages arriving, so it opened at the
  // oldest. The fixture's owner DM is two messages, too few to scroll, so
  // the hub's answer is repeated into a long one.
  step = 'private tail'
  const dm = '**/api/loops/gardener/conversation?conversation=owner_dm*'
  await page.route(dm, async (route) => {
    const msgs = await (await route.fetch()).json()
    const long = Array.from({ length: 15 }, (_, i) => msgs.map((m) => ({ ...m, id: m.id * 100 + i }))).flat()
    await route.fulfill({ json: long })
  })
  await page.goto(base + '/loops/gardener?private=owner_dm')
  await see(page.locator('.pane-scroll .timeline'), 'the owner DM')
  tail = await position()
  for (let tries = 0; tail.below >= 32 && tries < 20; tries++) {
    await sleep(250)
    tail = await position()
  }
  if (!tail.scrolls) throw new Error(`${step}: the conversation fits its pane, so the check proves nothing`)
  if (tail.below >= 32)
    throw new Error(`${step}: the owner DM opened ${Math.round(tail.below)}px above its newest message`)
  await page.unroute(dm)
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
  await see(page.getByRole('heading', { name: 'Claude logins', exact: true }), 'the Claude logins section')

  // Adding a Claude login (#1070) adds it signed out and opens its sign-in;
  // closed unfinished, it stays on the list to sign in later, and Remove
  // takes it off.
  step = 'Claude logins'
  const logins = page.locator('section.claude-login')
  await logins.getByRole('button', { name: 'Add a Claude login' }).click()
  const addLogin = page.getByRole('dialog', { name: 'Add a Claude login' })
  await addLogin.getByLabel('Name').fill('ada')
  await addLogin.getByRole('button', { name: 'Add and sign in' }).click()
  const loginSignIn = page.getByRole('dialog', { name: 'Sign in ada' })
  await see(loginSignIn, "the new login's sign-in")
  await loginSignIn.getByText('Close', { exact: true }).click()
  await gone(loginSignIn, "the new login's sign-in")
  const remove = logins.getByRole('button', { name: 'Remove the Claude login ada' })
  await see(remove, 'the new login on the list')
  page.once('dialog', (dialog) => dialog.accept())
  await remove.click()
  await gone(remove, 'the removed login')

  // Egress opens as a dialog from its summary (#971), and adding a host as a
  // second one over it, whose hint carries a chip.
  step = 'Egress'
  await page.getByRole('button', { name: 'Manage' }).click()
  const egress = page.getByRole('dialog', { name: 'Egress' })
  await see(egress.getByRole('button', { name: 'Add a host' }), 'the add-host button')
  const addHost = page.getByRole('dialog', { name: 'Add a host' })
  // Closing the dialog on top leaves the one under it open: a dialog's close
  // event bubbles through the React tree to the dialog it renders in.
  for (const [how, close] of [
    ['Escape', () => addHost.getByLabel('Host').press('Escape')],
    ['its ✕', () => addHost.getByRole('button', { name: 'Close' }).click()],
  ]) {
    await egress.getByRole('button', { name: 'Add a host' }).click()
    await see(addHost.getByLabel('Host'), 'the add-host field')
    await close()
    await gone(addHost, `Add a host, closed with ${how},`)
    await sleep(300)
    if (!(await egress.isVisible()))
      throw new Error(`${step}: closing Add a host with ${how} closed Egress too`)
  }
  await egress.getByRole('button', { name: 'Add a host' }).click()
  await see(addHost.getByLabel('Host'), 'the add-host field')

  // A literal to type copies on a click (#870): the clipboard holds the
  // command afterwards, and the chip says so. This one sits in a modal
  // dialog, where the rest of the page is inert.
  step = 'copy chip'
  const chip = addHost.getByRole('button', { name: 'Copy --egress-allow' })
  await chip.click()
  await see(page.locator('.copyable.copied'), 'the copied chip')
  const copied = await page.evaluate(() => globalThis.navigator.clipboard.readText())
  if (copied !== '--egress-allow') throw new Error(`${step}: the clipboard holds ${JSON.stringify(copied)}`)
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
  if (byCommand !== '--egress-allow') {
    throw new Error(`${step}: without the clipboard API the clipboard holds ${JSON.stringify(byCommand)}`)
  }
  await sleep(1600)
  await page.evaluate(() => {
    globalThis.document.execCommand = () => false
  })
  await chip.click()
  const title = await chip.getAttribute('title')
  const selected = await page.evaluate(() => String(globalThis.getSelection()))
  if (title !== 'Selected: press Ctrl+C' || selected !== '--egress-allow') {
    throw new Error(
      `${step}: with no way to copy, the chip said ${JSON.stringify(title)} and selected ${JSON.stringify(selected)}`,
    )
  }

  // Settings → Cloud (#865): the guided setup turns the cloud listener on,
  // for real, on a port beside the fixture hub's own, and checks an address
  // that can't resolve, so the check names the stage it stopped at. The
  // address goes in as Tailscale prints one, with a trailing slash and an
  // upper-case letter, which the hub stores normalized.
  step = 'Cloud'
  await page.goto(base + '/settings')
  await see(page.getByRole('heading', { name: 'Cloud', exact: true }), 'the Cloud section')
  await page.getByRole('button', { name: 'Set up cloud loops' }).click()
  const setup = page.getByRole('dialog', { name: 'Set up cloud loops' })
  const cloudListen = `127.0.0.1:${Number(new URL(base).port) + 2}`
  await setup.getByLabel('Listen on').fill(cloudListen)
  await setup.getByRole('button', { name: 'Turn on' }).click()
  await see(setup.getByText(`The cloud listener is on, at ${cloudListen}.`), 'the listener on')
  await setup.getByRole('button', { name: 'Next' }).click()
  await setup.getByLabel('Public address').fill('https://Spool.invalid/')
  await setup.getByRole('button', { name: 'Check', exact: true }).click()
  await see(setup.locator('.cloud-check .bad'), 'the stage the check stopped at')
  await see(setup.getByRole('button', { name: 'Check again' }), 'the address as the hub stored it')
  await setup.getByRole('button', { name: 'Close' }).click()
  await gone(setup, 'the setup dialog')
  await see(page.locator('.cloud-step.bad'), "the address's failing line")

  // The account step runs the CLI's own login (#864), which the fixture
  // hub's fakeclaude stands in for: a code it doesn't take ends the
  // sign-in, and one it does signs the hub's Claude login in. Settings →
  // Claude login signs it out (ADR-0058).
  const cloud = page.locator('section.cloud')
  const stepLine = (title) => cloud.locator('.cloud-step', { hasText: title })
  await stepLine('Claude login').getByRole('button', { name: 'Set up', exact: true }).click()
  await see(setup.getByRole('button', { name: 'Sign in with Claude ↗' }), 'the account step')
  const claudeTab = page.context().waitForEvent('page')
  await setup.getByRole('button', { name: 'Sign in with Claude ↗' }).click()
  await (await claudeTab).close()
  await setup.getByLabel('Code').fill('not-the-code')
  await setup.getByRole('button', { name: 'Sign in', exact: true }).click()
  await see(
    setup.getByText("wasn't accepted, and that sign-in has ended", { exact: false }),
    'a refused code',
  )
  await setup.getByRole('button', { name: 'Sign in with Claude ↗' }).click()
  await setup.getByLabel('Code').fill('fixture-code')
  await setup.getByRole('button', { name: 'Sign in', exact: true }).click()
  await see(setup.getByText('Signed in', { exact: false }), 'the account signed in')
  await setup.getByRole('button', { name: 'Close' }).click()
  await gone(setup, 'the setup dialog')

  // The environment step (ADR-0059): the fixture hub can't list
  // the account's environments, so the step falls back to pasting the ID,
  // and the setup is done only once the pasted list is confirmed.
  await stepLine('Environment').getByRole('button', { name: 'Set up', exact: true }).click()
  await see(setup.getByRole('button', { name: 'Copy the allowed domains' }), 'the list to paste')
  await setup.getByLabel('Environment ID').fill('env_01SmokeFixture')
  await setup.getByRole('button', { name: 'Save', exact: true }).click()
  await see(stepLine('Environment').getByText('allowlist not confirmed'), 'the unconfirmed environment')
  await setup.getByRole('button', { name: 'I pasted it' }).click()
  await see(setup.getByText("holds this hub's allowlist", { exact: false }), 'the confirmed list')
  await setup.getByRole('button', { name: 'Close' }).click()
  await gone(setup, 'the setup dialog')
  await see(stepLine('Environment').getByText('allowlist pasted'), 'the confirmed environment')
  const login = page.locator('section.claude-login')
  page.once('dialog', (dialog) => dialog.accept())
  await login.getByRole('button', { name: 'Sign out of the Claude login' }).click()
  await see(login.getByText('not signed in'), 'the Claude login signed out')

  // New loop offers Cloud on every hub (#867). This one isn't set up for
  // cloud loops, so picking it opens the guided setup, and the form won't
  // create one until the setup is done.
  step = 'New loop, cloud'
  await page.goto(base + '/new')
  await heading('New loop')
  await page.getByLabel('Name').fill('gardener')
  await page.getByLabel('Mission').fill('Tend the handbook.')
  await page.getByLabel('Runtime').selectOption('cloud')
  await see(setup, 'the guided setup')
  await setup.getByRole('button', { name: 'Close' }).click()
  await gone(setup, 'the setup dialog')
  await see(page.getByText('This hub is not set up for cloud loops yet.'), 'the cloud runtime not ready')
  await see(
    page.getByText('Cloud loops are not isolated from each other', { exact: false }),
    'the trust warning',
  )
  await see(
    page.getByText('attach a GH_TOKEN connection on its page and pause and resume it', { exact: false }),
    'the cloud note in place of the repo field',
  )
  if (await page.getByRole('button', { name: 'Create loop' }).isEnabled())
    throw new Error('Create loop is enabled for a cloud loop on a hub not set up for one')

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
