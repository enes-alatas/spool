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
// The Activity page is never shot, here or by hand: it carries the operator's
// own DMs. There is no flag for it.
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
  { name: 'loop-timeline', path: '/loops/gardener', wait: '.timeline' },
  { name: 'new-loop', path: '/new', wait: '#nl-runtime' },
  { name: 'access', path: '/access', wait: '.feed-item' },
  { name: 'rules', path: '/rules', wait: '.page' },
  { name: 'settings', path: '/settings', wait: '.page' },
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

await browser.close()
for (const file of taken) console.log(file)
