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
  { name: 'loop-timeline', path: '/loops/gardener', wait: '.timeline' },
  { name: 'new-loop', path: '/new', wait: '#nl-runtime' },
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

// Secrets is a section of a loop's page rather than a page of its own, so it
// is shot as the card: a full-page shot of the loop would bury it.
await page.goto(base + '/loops/gardener', { waitUntil: 'networkidle' })
const secrets = page.locator('.side-panel', { has: page.locator('h3', { hasText: /^Secrets$/ }) }).first()
// Fail the way the other shots do: a set silently one image short is worse
// than no set, because the missing one is the one nobody counts.
await secrets.waitFor({ timeout: 15000 })
const secretsFile = `${outDir}/secrets.png`
await secrets.screenshot({ path: secretsFile })
taken.push(secretsFile)

// The mission editor is a state of a panel rather than a page, and it is the
// state worth shooting: the read-only mission is already in the loop shot.
const mission = page.locator('.side-panel', { has: page.locator('h3', { hasText: /^Mission$/ }) }).first()
await mission.waitFor({ timeout: 15000 })
await mission.getByRole('button', { name: 'Edit the mission' }).click()
await mission.locator('textarea').waitFor({ timeout: 15000 })
const missionFile = `${outDir}/mission-edit.png`
await mission.screenshot({ path: missionFile })
taken.push(missionFile)

await browser.close()
for (const file of taken) console.log(file)
