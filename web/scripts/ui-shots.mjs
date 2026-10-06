// Takes the control room's standard screenshot set against a fixture store.
//
// A screenshot in a PR is driven against fixture data, never the live fleet
// (CONVENTIONS.md): the timeline renders raw assistant text and tool inputs,
// so a live shot publishes whatever a loop echoed. That rule had no tooling
// behind it — the documented path was "point a dev server at something and
// press the button" — which is how thirteen live-data images reached the
// board before the go-public audit found them (#153, #248).
//
// `make ui-shots` runs this against a hub started on a directory that
// `cmd/uifixture` wrote seconds earlier, so the fleet in every shot is
// invented by construction rather than by the care of whoever took it.
//
// A loop's Undelivered pane carries the full body of every message that
// failed to send, an undelivered owner DM included, and it is in the set
// below because being in the set is what makes a shot of it safe: the fleet
// it renders is one `cmd/uifixture` invented seconds earlier. Shot here,
// never by hand.
//
// The Activity page carries the same kind of text and is never shot, here or
// by hand, and there is no flag for it. That is the operator's decision
// (fleet rules; CONVENTIONS, "Screenshots come from fixtures", #277), not a
// gap in the argument above: the page that is all the operator's own
// conversations stays out of every picture, so nobody has to ask whether a
// shot of it came from the fixture.
import { chromium } from 'playwright'
import { mkdirSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const base = process.env.SPOOL_URL ?? 'http://127.0.0.1:5261'
const dataDir = process.env.SPOOL_DATA_DIR
// Relative to web/, not to the caller: `make ui-shots` runs this from web/
// and a hand run may be from anywhere, and a set of shots that lands in a
// different directory depending on where it was started is a set nobody finds.
const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const outDir = process.env.SPOOL_SHOTS_DIR ? resolve(process.env.SPOOL_SHOTS_DIR) : resolve(webDir, 'shots')
if (!dataDir) {
  console.error('ui-shots: SPOOL_DATA_DIR is required (the fixture hub’s data directory)')
  process.exit(2)
}
// Read, never printed: the operator token the hub minted for the fixture hub.
const token = readFileSync(`${dataDir}/operator-token`, 'utf8').trim()

mkdirSync(outDir, { recursive: true })
const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: 1180, height: 900 }, deviceScaleFactor: 2 })
const page = await ctx.newPage()

await page.goto(base + '/', { waitUntil: 'networkidle' })
if (await page.isVisible('#operator-token')) {
  await page.fill('#operator-token', token)
  await page.click('button[type=submit]')
  await page.waitForSelector('.topbar', { timeout: 15000 })
}

const shots = [
  { name: 'fleet', path: '/', wait: '.fleet-row' },
  // The Fleet page's channel tab: operator, loop and human posts, a reply's
  // quote, and what each reached (#286).
  { name: 'channel', path: '/?view=channel', wait: '.knot' },
  // A card per channel, with the fixture's loops in them (#484).
  { name: 'channels', path: '/channels', wait: '.channel-loop' },
  { name: 'loop-timeline', path: '/loops/gardener', wait: '.timeline' },
  { name: 'new-loop', path: '/new', wait: '#nl-runtime' },
  // One connection of each shape the page draws, with and without a secret
  // (#506), a rotated one and a revoked one (#507); the create form is under
  // them.
  { name: 'connections', path: '/connections', wait: '.connection-row' },
  { name: 'access', path: '/access', wait: '.feed-item' },
  { name: 'rules', path: '/rules', wait: '.page' },
  { name: 'settings', path: '/settings', wait: '.page' },
  // The archivist, because the fixture gives it two failed sends to two
  // destinations: the pane is empty-by-design on a healthy loop, and one row
  // cannot show whether the columns line up (#282).
  { name: 'undelivered', path: '/loops/archivist?pane=undelivered', wait: '.undelivered-row' },
]

const taken = []
for (const shot of shots) {
  await page.goto(base + shot.path, { waitUntil: 'networkidle' })
  await page.waitForSelector(shot.wait, { timeout: 15000 })
  const file = `${outDir}/${shot.name}.png`
  await page.screenshot({ path: file, fullPage: true })
  taken.push(file)
}

await page.goto(base + '/loops/gardener', { waitUntil: 'networkidle' })

// The gardener's attached connections and the pick-list for the one left
// (#506), shot as the card: a full-page shot of the loop would bury it. Its
// History is open, since the record is what the panel cannot show shut
// (#507).
const connections = page
  .locator('.side-panel', { has: page.locator('h3', { hasText: /^Connections$/ }) })
  .first()
await connections.waitFor({ timeout: 15000 })
await connections.getByRole('button', { name: 'History' }).click()
await connections.locator('.connection-event').first().waitFor({ timeout: 15000 })
const connectionsFile = `${outDir}/loop-connections.png`
await connections.screenshot({ path: connectionsFile })
taken.push(connectionsFile)

// The mission editor is a state of a panel rather than a page, and it is the
// state worth shooting: the read-only mission is already in the loop shot.
const mission = page.locator('.side-panel', { has: page.locator('h3', { hasText: /^Mission$/ }) }).first()
await mission.waitFor({ timeout: 15000 })
await mission.getByRole('button', { name: 'Edit the mission' }).click()
await mission.locator('textarea').waitFor({ timeout: 15000 })
const missionFile = `${outDir}/mission-edit.png`
await mission.screenshot({ path: missionFile })
taken.push(missionFile)

// The first-run page (#581) in the states its cards draw. The fixture
// hub has done all three pillars, so it opens on Fleet; these answers stand
// in for a hub that has not, worded as #580's reasons are. Every other read
// on the page is still the fixture's.
const firstRun = {
  none: {
    completed: false,
    harness: { done: false, reason: 'no setup-token saved in Settings' },
    surface: { done: false, reason: 'no chat surface has carried a message yet' },
    loops: { done: false, reason: 'no loops' },
  },
  // The harness's login check (#588): refused, and its Check now beside the
  // token button.
  refused: {
    completed: false,
    harness: { done: false, reason: 'the login check was refused: OAuth token has expired' },
    surface: { done: false, reason: 'no chat surface has carried a message yet' },
    loops: { done: false, reason: 'no loops' },
  },
  two: {
    completed: false,
    harness: { done: true, reason: 'a turn authenticated' },
    surface: { done: false, reason: 'no message received from the operator on telegram yet' },
    loops: { done: true },
  },
  all: {
    completed: false,
    harness: { done: true, reason: 'a turn authenticated' },
    surface: { done: true },
    loops: { done: true },
  },
}
// The fixture hub is bare, whose Harness card has no token to ask for; the
// card and its dialog are shot as a docker hub's, with a token saved once
// the harness says so.
const dockerSettings = (tokenSet) => async (route) => {
  const settings = await (await route.fetch()).json()
  await route.fulfill({ json: { ...settings, default_runtime: 'docker', claude_token_set: tokenSet } })
}
for (const [state, body] of Object.entries(firstRun)) {
  await page.route('**/api/settings', dockerSettings(state !== 'none'))
  await page.route('**/api/onboarding', (route) => route.fulfill({ json: body }))
  await page.goto(base + '/', { waitUntil: 'networkidle' })
  await page.waitForSelector('.pillars', { timeout: 15000 })
  const file = `${outDir}/first-run-${state}.png`
  await page.screenshot({ path: file, fullPage: true })
  taken.push(file)
  await page.unroute('**/api/onboarding')
  await page.unroute('**/api/settings')
}

// The Harness card's token dialog (#588), opened over the first-run page.
await page.route('**/api/settings', dockerSettings(false))
await page.route('**/api/onboarding', (route) => route.fulfill({ json: firstRun.none }))
await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.locator('.pillar').first().locator('.pillar-actions .btn').first().click()
await page.locator('.step-dialog').waitFor({ timeout: 15000 })
const tokenDialogFile = `${outDir}/first-run-token-dialog.png`
await page.screenshot({ path: tokenDialogFile })
taken.push(tokenDialogFile)
await page.unroute('**/api/settings')

// Settings' login check row (#588), under the token controls, after a refusal.
await page.route('**/api/onboarding', (route) => route.fulfill({ json: firstRun.refused }))
await page.goto(base + '/settings', { waitUntil: 'networkidle' })
const tokenForm = page.locator('.form', { has: page.locator('#claude-token') })
await tokenForm.locator('.login-check').waitFor({ timeout: 15000 })
const loginCheckFile = `${outDir}/settings-login-check.png`
await tokenForm.screenshot({ path: loginCheckFile })
taken.push(loginCheckFile)
await page.unroute('**/api/onboarding')

// The Loops and Chat surface cards' dialogs (#589), opened over the
// first-run page. The fixture's loops all carry Telegram bots; the attach
// shot strips them so the dialog offers the surface choice, and the pair
// shot stands in a pending sender for the operator's first message.
await page.route('**/api/onboarding', (route) => route.fulfill({ json: firstRun.refused }))
await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.getByRole('button', { name: 'New loop', exact: true }).first().click()
await page.locator('.step-dialog #nl-name').waitFor({ timeout: 15000 })
await page.fill('#nl-name', 'aster')
const loopDialogFile = `${outDir}/first-run-loop-dialog.png`
await page.screenshot({ path: loopDialogFile })
taken.push(loopDialogFile)
await page.unroute('**/api/onboarding')

await page.route('**/api/onboarding', (route) => route.fulfill({ json: firstRun.two }))
await page.route('**/api/loops', async (route) => {
  const loops = await (await route.fetch()).json()
  await route.fulfill({
    json: loops.map((l) => ({ ...l, surface: '', has_tg_token: false, tg_bot_username: '' })),
  })
})
await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.getByRole('button', { name: 'Attach a bot' }).click()
await page.locator('.surface-dialog').waitFor({ timeout: 15000 })
const surfaceAttachFile = `${outDir}/first-run-surface-attach.png`
await page.screenshot({ path: surfaceAttachFile })
taken.push(surfaceAttachFile)
await page.unroute('**/api/loops')

const now = Date.now()
await page.route('**/api/telegram/senders', (route) =>
  route.fulfill({
    json: [
      {
        tg_user_id: 4242,
        username: 'operator_fixture',
        display: 'Operator',
        status: 'pending',
        pair_code: 'K7Q2PX',
        first_seen_via: 'aster',
        created_at: now,
        updated_at: now,
      },
    ],
  }),
)
await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.getByRole('button', { name: 'Attach a bot' }).click()
await page.locator('.surface-dialog .feed-item').waitFor({ timeout: 15000 })
const surfacePairFile = `${outDir}/first-run-surface-pair.png`
await page.screenshot({ path: surfacePairFile })
taken.push(surfacePairFile)
await page.unroute('**/api/telegram/senders')
await page.unroute('**/api/onboarding')

// A Slack loop's rooms panel (#548). The fixture's loops are all on
// Telegram, so the gardener is shot as a Slack loop, with an unbound Slack
// channel waiting above its fleet room.
const slackLoop = (loop) => ({
  ...loop,
  surface: 'slack',
  has_tg_token: false,
  tg_bot_username: '',
  has_slack_tokens: true,
})
await page.route('**/api/loops/gardener', async (route) => {
  if (route.request().method() !== 'GET') return route.continue()
  await route.fulfill({ json: slackLoop(await (await route.fetch()).json()) })
})
await page.route('**/api/loops/gardener/rooms', (route) =>
  route.fulfill({
    json: [
      {
        surface: 'slack',
        room_id: 'C07RELEASE1',
        title: 'release-notes',
        channel: '',
        first_seen_at: Date.now(),
      },
      {
        surface: 'slack',
        room_id: 'C07GENERAL0',
        title: 'general',
        channel: 'group',
        first_seen_at: Date.now(),
      },
    ],
  }),
)
await page.goto(base + '/loops/gardener', { waitUntil: 'networkidle' })
await page.locator('#rooms').waitFor({ timeout: 15000 })
const slackRoomsFile = `${outDir}/slack-rooms.png`
await page.locator('#rooms').screenshot({ path: slackRoomsFile })
taken.push(slackRoomsFile)
await page.unroute('**/api/loops/gardener/rooms')
await page.unroute('**/api/loops/gardener')

await browser.close()
for (const file of taken) console.log(file)
