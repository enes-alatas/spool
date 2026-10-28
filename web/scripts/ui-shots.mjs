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
// Read, never printed: the password the fixture gave its users (#674).
const password = readFileSync(`${dataDir}/fixture-user-password`, 'utf8').trim()

mkdirSync(outDir, { recursive: true })
const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: 1180, height: 900 }, deviceScaleFactor: 2 })
const page = await ctx.newPage()

// The sign-in page and the change a one-time password gets (#674), each
// in a context of its own: robin's session must not be the one the rest
// of the shots run in.
const signedOut = await browser.newContext({ viewport: { width: 1180, height: 760 }, deviceScaleFactor: 2 })
const door = await signedOut.newPage()
await door.goto(base + '/', { waitUntil: 'networkidle' })
await door.locator('#username').waitFor({ timeout: 15000 })
const signinFile = `${outDir}/signin.png`
await door.locator('.login-card').screenshot({ path: signinFile })
await door.fill('#username', 'robin')
await door.fill('#password', password)
await door.click('button[type=submit]')
await door.locator('#new-password').waitFor({ timeout: 15000 })
const changeFile = `${outDir}/change-password.png`
await door.locator('.login-card').screenshot({ path: changeFile })
await signedOut.close()

// The member view (#679), as sam: Fleet with no New loop, a loop's page
// with Wake now and the composer as its only actions, Connections by name
// and kind with no config (#924), and a Settings that is the account and
// the build.
const memberCtx = await browser.newContext({ viewport: { width: 1180, height: 900 }, deviceScaleFactor: 2 })
const member = await memberCtx.newPage()
await member.goto(base + '/', { waitUntil: 'networkidle' })
await member.fill('#username', 'sam')
await member.fill('#password', password)
await member.click('button[type=submit]')
await member.waitForSelector('.topbar', { timeout: 15000 })
const memberShots = [
  { name: 'member-fleet', path: '/', wait: '.fleet-row' },
  { name: 'member-loop', path: '/loops/gardener', wait: '.side-panel' },
  { name: 'member-connections', path: '/connections', wait: '.connection-row' },
  { name: 'member-settings', path: '/settings', wait: '.account-name' },
]
const memberFiles = []
for (const shot of memberShots) {
  await member.goto(base + shot.path, { waitUntil: 'networkidle' })
  await member.waitForSelector(shot.wait, { timeout: 15000 })
  const file = `${outDir}/${shot.name}.png`
  await member.screenshot({ path: file, fullPage: true })
  memberFiles.push(file)
}
// sam's chat accounts (#704, #745): linked on Telegram by the fixture, and
// waiting for YES on Slack, a code of theirs having reached the gardener's
// app.
const accountSection = async (file) => {
  const head = await member.locator('h2', { hasText: 'Account' }).boundingBox()
  const end = await member.locator('.links').boundingBox()
  await member.screenshot({
    path: file,
    clip: { x: 0, y: head.y - 16, width: 1180, height: end.y + end.height - head.y + 32 },
  })
  memberFiles.push(file)
}
await member.goto(base + '/settings', { waitUntil: 'networkidle' })
await member.locator('.link-waiting').waitFor({ timeout: 15000 })
await accountSection(`${outDir}/settings-links.png`)
// The link dialog, from a fresh code to the step that waits for YES. The
// hub's own code is random, so the shot shows the fixture's in its place;
// the question is held back until the code is open, then let through as the
// fixture seeded it.
await member.route('**/api/me/links/waiting', (route) => route.fulfill({ json: [] }))
await member.route('**/api/me/links/code', (route) =>
  route.fulfill({ status: 201, json: { code: 'link-fixture0002', expires_at: Date.now() + 15 * 60000 } }),
)
await member.reload({ waitUntil: 'networkidle' })
await member.getByRole('button', { name: 'Link Slack' }).click()
const linkDialog = member.locator('dialog.link-dialog')
await linkDialog.locator('.otp-value').waitFor({ timeout: 15000 })
const linkCodeFile = `${outDir}/settings-link-code.png`
await member.screenshot({ path: linkCodeFile })
memberFiles.push(linkCodeFile)
await member.unroute('**/api/me/links/waiting')
await linkDialog.locator('li.now', { hasText: 'Reply YES there' }).waitFor({ timeout: 15000 })
const linkWaitingFile = `${outDir}/settings-link-waiting.png`
await member.screenshot({ path: linkWaitingFile })
memberFiles.push(linkWaitingFile)
// The gardener's page as sam (#723): their own private conversation is a
// tab, and the owner DM with rana is not.
await member.goto(base + '/loops/gardener?private=dm:sam_example', { waitUntil: 'networkidle' })
await member.locator('.private-note').waitFor({ timeout: 15000 })
const memberPrivateFile = `${outDir}/member-loop-private.png`
await member.screenshot({ path: memberPrivateFile })
memberFiles.push(memberPrivateFile)
await memberCtx.close()

await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.fill('#username', 'admin')
await page.fill('#password', password)
await page.click('button[type=submit]')
await page.waitForSelector('.topbar', { timeout: 15000 })
// Settings' Account section, signed in as admin (#674).
await page.goto(base + '/settings', { waitUntil: 'networkidle' })
await page.locator('.account .account-name').waitFor({ timeout: 15000 })
const accountFile = `${outDir}/settings-account.png`
await page.locator('.account').screenshot({ path: accountFile })

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
  { name: 'rules', path: '/rules', wait: '.page' },
  { name: 'settings', path: '/settings', wait: '.page' },
  // The archivist, because the fixture gives it two failed sends to two
  // destinations: the pane is empty-by-design on a healthy loop, and one row
  // cannot show whether the columns line up (#282).
  { name: 'undelivered', path: '/loops/archivist?pane=undelivered', wait: '.undelivered-row' },
]

const taken = [signinFile, changeFile, accountFile, ...memberFiles]

// The gardener's page as admin (#723): a tab for each private
// conversation, sam's open, read only.
await page.goto(base + '/loops/gardener?private=dm:sam_example', { waitUntil: 'networkidle' })
await page.locator('.private-note').waitFor({ timeout: 15000 })
const loopPrivateFile = `${outDir}/loop-private.png`
await page.screenshot({ path: loopPrivateFile })
taken.push(loopPrivateFile)
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

// The mission editor is the state worth shooting: the read-only mission is
// already in the loop shot. A dialog over the page since #847, so the shot is
// the page, and Cancel closes it: a modal left open takes every click after.
const mission = page.locator('.side-panel', { has: page.locator('h3', { hasText: /^Mission$/ }) }).first()
await mission.waitFor({ timeout: 15000 })
await mission.getByRole('button', { name: 'Edit the mission' }).click()
const missionDialog = page.locator('dialog.mission-dialog')
await missionDialog.locator('textarea').waitFor({ timeout: 15000 })
const missionFile = `${outDir}/mission-edit.png`
await page.screenshot({ path: missionFile })
taken.push(missionFile)
await missionDialog.getByRole('button', { name: 'Cancel' }).click()
await missionDialog.waitFor({ state: 'detached', timeout: 15000 })

// A bare loop's way into a docker container (#632), shot as its confirm:
// what the move keeps and what it leaves on the host. The panel is the
// courier's, the fixture's bare loop, and the viewport grows to hold it.
await page.setViewportSize({ width: 1180, height: 1400 })
await page.goto(base + '/loops/courier', { waitUntil: 'networkidle' })
const courierWorkstation = page
  .locator('.side-panel', { has: page.locator('h3', { hasText: /^Workstation$/ }) })
  .first()
await courierWorkstation.getByRole('button', { name: 'Move into a docker container' }).click()
await courierWorkstation.locator('.rehome-confirm').waitFor({ timeout: 15000 })
const rehomeFile = `${outDir}/rehome-confirm.png`
await courierWorkstation.screenshot({ path: rehomeFile })
taken.push(rehomeFile)
await page.setViewportSize({ width: 1180, height: 900 })

// Settings' Egress section (#542), shot as the section, a summary since
// #971: Settings scrolls in its own container, so a page shot cuts it off,
// and the viewport grows to hold all of it. The fixture hub runs no egress
// image, so as served it draws the open state; the walled one is the same
// view with the proxy's answer patched in, as a docker hub serves it, shot
// as the dialog Manage opens.
await page.setViewportSize({ width: 1180, height: 1900 })
await page.goto(base + '/settings', { waitUntil: 'networkidle' })
const egress = page.locator('section.egress')
await egress.locator('.egress-summary').waitFor({ timeout: 15000 })
const egressOpenFile = `${outDir}/settings-egress-open.png`
await egress.screenshot({ path: egressOpenFile })
taken.push(egressOpenFile)
await page.route('**/api/settings/egress', async (route) => {
  const view = await (await route.fetch()).json()
  const gateway = {
    reason:
      "The hub: the loop's MCP tools and hooks reach Spool on this one port of your machine, and on no other.",
    hosts: ['host.docker.internal:8081'],
  }
  await route.fulfill({
    json: { ...view, enforced: true, built_in: [...view.built_in, gateway], applied_at: Date.now() },
  })
})
await page.goto(base + '/settings', { waitUntil: 'networkidle' })
await egress.getByRole('button', { name: 'Manage' }).click()
const egressDialog = page.locator('.egress-dialog')
await egressDialog.locator('.egress-status.ok').waitFor({ timeout: 15000 })
const egressFile = `${outDir}/settings-egress.png`
await egressDialog.screenshot({ path: egressFile })
taken.push(egressFile)
await egressDialog.getByRole('button', { name: 'Close' }).click()
await page.unroute('**/api/settings/egress')

// Settings' Users and access section (#691, #732), as the owner admin sees
// the fixture's users: robin still on a one-time password, admin the last
// owner, whose own role and Remove are disabled with the reason, sam linked,
// and mia linked and blocked. Under them, the senders not linked to a user.
// The ignored strangers and a pause live in the hub's memory, which the
// fixture cannot seed (#744), so the route answers with invented ones: two
// Telegram strangers and one Slack one, and Telegram's linking paused.
await page.route('**/api/strangers', (route) =>
  route.fulfill({
    json: {
      window_hours: 24,
      surfaces: [
        {
          surface: 'telegram',
          count: 2,
          ids: ['700000011', '700000012'],
          more: false,
          paused_until: Date.now() + 42 * 60000,
        },
        { surface: 'slack', count: 1, ids: ['U0FIXTURE9'], more: false, paused_until: 0 },
      ],
    },
  }),
)
const usersSection = page.locator('section.users')
await page.goto(base + '/settings', { waitUntil: 'networkidle' })
await usersSection.locator('.user-row').first().waitFor({ timeout: 15000 })
const usersFile = `${outDir}/settings-users.png`
await usersSection.screenshot({ path: usersFile })
taken.push(usersFile)
// On a phone, where each row's actions take a line under the name.
await page.setViewportSize({ width: 375, height: 1900 })
await usersSection.locator('.user-row').first().waitFor({ timeout: 15000 })
const usersNarrowFile = `${outDir}/settings-users-narrow.png`
await usersSection.screenshot({ path: usersNarrowFile })
taken.push(usersNarrowFile)
await page.setViewportSize({ width: 1180, height: 900 })
await page.unroute('**/api/strangers')

// Who changed a loop (#696): who made it, under its name, and the Changes
// panel with the fixture's operator and admin changes to the gardener.
await page.goto(base + '/loops/gardener', { waitUntil: 'networkidle' })
await page.locator('.loop-provenance').waitFor({ timeout: 15000 })
const provenance = await page.locator('.loop-provenance').boundingBox()
const loopMakerFile = `${outDir}/loop-provenance.png`
await page.screenshot({
  path: loopMakerFile,
  clip: { x: 0, y: 0, width: 1180, height: provenance.y + provenance.height + 16 },
})
taken.push(loopMakerFile)
const changesPanel = page.locator('.side-panel').filter({ has: page.locator('h3', { hasText: /^Changes$/ }) })
await changesPanel.locator('.row').first().waitFor({ timeout: 15000 })
const loopChangesFile = `${outDir}/loop-changes.png`
await changesPanel.screenshot({ path: loopChangesFile })
taken.push(loopChangesFile)
// Activity with the changes among the messages, from fixture data only.
await page.goto(base + '/activity', { waitUntil: 'networkidle' })
const changeRows = page.locator('.feed-item.change-item')
await changeRows.first().waitFor({ timeout: 15000 })
// Clipped from the message above the first change to the fourth change,
// which is the part this adds; the rest is the messages feed. The
// room scrolls inside its frame rather than the document, so the viewport
// grows until the feed needs no scrolling at all.
await page.setViewportSize({ width: 1180, height: 6000 })
const rows = await page.locator('.feed-item').evaluateAll((all) =>
  all.map((el) => {
    const box = el.getBoundingClientRect()
    return { change: el.classList.contains('change-item'), top: box.top, bottom: box.bottom }
  }),
)
const first = rows.findIndex((r) => r.change)
// The fourth change: past it the feed runs back through the fixture's
// older record, a run of changes and nothing else.
const last = rows.findIndex((r, i) => r.change && rows.slice(0, i).filter((p) => p.change).length === 3)
const top = rows[Math.max(first - 1, 0)].top - 8
const bottom = rows[last].bottom + 8
const activityFile = `${outDir}/activity-changes.png`
await page.screenshot({ path: activityFile, clip: { x: 0, y: top, width: 1180, height: bottom - top } })
await page.setViewportSize({ width: 1180, height: 900 })
taken.push(activityFile)

// The Fleet page's plan strip (#648) in the states the fixture cannot hold
// at once: near both limits, a hub no loop has reported the plan's usage
// to yet, and a hub with three Claude logins, one of them not reported
// yet (#1074). The fixture's own reading is in the fleet shot. Clipped to the
// header, the strip and the column heads, which is all that changes.
const planStates = {
  hot: (view) => ({
    ...view,
    five_hour: { used_percent: 93, resets_at: Date.now() + 41 * 60000 },
    seven_day: { used_percent: 72, resets_at: view.seven_day.resets_at },
  }),
  unknown: () => ({ five_hour: null, seven_day: null, unknown: "no loop has reported the plan's usage yet" }),
  logins: (view) => ({
    ...view,
    logins: [
      { name: 'default', ...view },
      {
        name: 'ada',
        five_hour: { used_percent: 78, resets_at: Date.now() + 66 * 60000 },
        seven_day: { used_percent: 64, resets_at: view.seven_day.resets_at },
        as_of: Date.now() - 25 * 60000,
        cap: null,
      },
      {
        name: 'robin',
        five_hour: null,
        seven_day: null,
        unknown: "no loop has reported the plan's usage yet",
      },
    ],
  }),
}
for (const [state, render] of Object.entries(planStates)) {
  await page.route('**/api/plan-usage', async (route) => {
    route.fulfill({ json: render(await (await route.fetch()).json()) })
  })
  await page.goto(base + '/', { waitUntil: 'networkidle' })
  await page.locator('.plan-strip .plan-title').waitFor({ timeout: 15000 })
  const head = await page.locator('.fleet-cols').boundingBox()
  const file = `${outDir}/fleet-plan-${state}.png`
  await page.screenshot({ path: file, clip: { x: 0, y: 0, width: 1180, height: head.y + head.height + 8 } })
  taken.push(file)
  await page.unroute('**/api/plan-usage')
}

// The plan cap (#651) on a hub whose 5-hour window has passed its
// threshold: the Fleet banner with Resume now, capped rows waking at the
// reset and a loop finishing its turn first, then the Settings section over
// the same read. The fixture hub has no cap to fire, so the reads are
// patched as a capped hub serves them.
const capUntil = Date.now() + 41 * 60000
await page.route('**/api/plan-usage', async (route) => {
  const view = await (await route.fetch()).json()
  route.fulfill({
    json: {
      ...view,
      five_hour: { used_percent: 93, resets_at: capUntil, cap_percent: 90 },
      seven_day: { ...view.seven_day, cap_percent: 90 },
      cap: { windows: ['five_hour'], until: capUntil },
    },
  })
})
await page.route('**/api/loops', async (route) => {
  const loops = await (await route.fetch()).json()
  route.fulfill({
    json: loops.map((loop, i) =>
      i === 1 ? { ...loop, state: 'busy' } : { ...loop, state: 'capped', capped_until: capUntil },
    ),
  })
})
await page.route('**/api/settings', async (route) => {
  const settings = await (await route.fetch()).json()
  route.fulfill({ json: { ...settings, plan_cap_five_hour_percent: 90, plan_cap_seven_day_percent: 90 } })
})
await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.locator('.cap-banner').waitFor({ timeout: 15000 })
const fleetCappedFile = `${outDir}/fleet-capped.png`
await page.screenshot({ path: fleetCappedFile, clip: { x: 0, y: 0, width: 1180, height: 560 } })
taken.push(fleetCappedFile)
await page.goto(base + '/settings', { waitUntil: 'networkidle' })
const guardrails = page.locator('section.plan-guardrails')
await guardrails.locator('.cap-banner').waitFor({ timeout: 15000 })
const guardrailsFile = `${outDir}/settings-plan-guardrails.png`
await guardrails.screenshot({ path: guardrailsFile })
taken.push(guardrailsFile)
// A capped loop's own pane says why its wake is the window's reset.
const cappedLoop = /\/api\/loops\/gardener$/
await page.route(cappedLoop, async (route) => {
  const loop = await (await route.fetch()).json()
  route.fulfill({ json: { ...loop, state: 'capped', capped_until: capUntil } })
})
await page.goto(base + '/loops/gardener', { waitUntil: 'networkidle' })
const wakePanel = page.locator('aside .side-panel').first()
await wakePanel.locator('.wake-note').waitFor({ timeout: 15000 })
const loopCappedFile = `${outDir}/loop-capped.png`
await wakePanel.screenshot({ path: loopCappedFile })
taken.push(loopCappedFile)
await page.unroute(cappedLoop)
await page.unroute('**/api/loops')
await page.unroute('**/api/plan-usage')
// The same cap on one Claude login of three (#1075, #1076): its ring wears
// a red rim and its details name the loops on it that sleep, in the
// banner's place, and Settings says where each other login stands.
await page.route('**/api/plan-usage', async (route) => {
  const view = await (await route.fetch()).json()
  const ada = {
    five_hour: { used_percent: 93, resets_at: capUntil, cap_percent: 90 },
    seven_day: { used_percent: 64, resets_at: view.seven_day.resets_at, cap_percent: 90 },
    as_of: Date.now() - 3 * 60000,
    cap: { windows: ['five_hour'], until: capUntil },
  }
  const unknown = "no loop has reported the plan's usage yet"
  route.fulfill({
    json: {
      ...view,
      logins: [
        { name: 'default', ...view },
        { name: 'ada', ...ada },
        { name: 'robin', five_hour: null, seven_day: null, unknown, cap: null },
      ],
    },
  })
})
await page.route('**/api/loops', async (route) => {
  const loops = await (await route.fetch()).json()
  route.fulfill({
    json: loops.map((loop, i) =>
      i === 0
        ? { ...loop, claude_login: 'ada', state: 'capped', capped_until: capUntil }
        : i === 1
          ? { ...loop, claude_login: 'ada', state: 'busy' }
          : loop,
    ),
  })
})
await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.getByRole('button', { name: 'Plan usage of the Claude login ada' }).click()
await page.locator('.login-ring-capped-note').waitFor({ timeout: 15000 })
const loginCappedFile = `${outDir}/fleet-login-capped.png`
await page.screenshot({ path: loginCappedFile, clip: { x: 0, y: 0, width: 1180, height: 560 } })
taken.push(loginCappedFile)
await page.goto(base + '/settings', { waitUntil: 'networkidle' })
await guardrails.locator('.cap-banner').waitFor({ timeout: 15000 })
const guardrailsLoginFile = `${outDir}/settings-plan-guardrails-logins.png`
await guardrails.screenshot({ path: guardrailsLoginFile })
taken.push(guardrailsLoginFile)
await page.unroute('**/api/settings')
await page.unroute('**/api/loops')
await page.unroute('**/api/plan-usage')
// A loop at its own daily cap (#652), the fleet's plan cap not holding:
// the pane names the cap and that raising it past today's spend, or
// removing it, wakes the loop, the chips to change it under that spend.
const midnight = new Date()
midnight.setHours(24, 0, 0, 0)
await page.route(cappedLoop, async (route) => {
  const loop = await (await route.fetch()).json()
  route.fulfill({
    json: {
      ...loop,
      state: 'capped',
      capped_until: midnight.getTime(),
      cost_today_usd: 5.08,
      daily_cap_usd: 5,
      daily_cap_reached: true,
    },
  })
})
await page.goto(base + '/loops/gardener', { waitUntil: 'networkidle' })
await wakePanel.locator('.cap-chips').waitFor({ timeout: 15000 })
const loopDailyCapFile = `${outDir}/loop-daily-capped.png`
await wakePanel.screenshot({ path: loopDailyCapFile })
taken.push(loopDailyCapFile)
await page.unroute(cappedLoop)

// The first-run page (#581) in the states its cards draw. The fixture
// hub has done all three pillars, so it opens on Fleet; these answers stand
// in for a hub that has not, worded as #580's reasons are. Every other read
// on the page is still the fixture's.
const firstRun = {
  none: {
    completed: false,
    harness: { done: false, reason: 'no Claude login or setup-token in Settings' },
    surface: { done: false, reason: 'no chat surface has carried a message yet' },
    loops: { done: false, reason: 'no loops' },
  },
  // The harness's login check (#588): refused, and its Check now beside the
  // sign-in button.
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
  // A step the hub is working on (#661): the first loop on its first turn,
  // and then answering the operator's first message.
  'first-turn': {
    completed: false,
    harness: { done: true, reason: 'a turn authenticated' },
    surface: { done: false, reason: 'no chat surface has carried a message yet' },
    loops: { done: false, reason: 'no loop has woken', progress: 'first_turn', progress_loop: 'scout' },
  },
  answering: {
    completed: false,
    harness: { done: true, reason: 'a turn authenticated' },
    surface: {
      done: false,
      reason: "no loop's message has reached telegram yet",
      progress: 'answering',
      progress_loop: 'scout',
    },
    loops: { done: true },
  },
  all: {
    completed: false,
    harness: { done: true, reason: 'a turn authenticated' },
    surface: { done: true },
    loops: { done: true },
  },
}
// The fixture hub is bare, whose Harness card has no sign-in to ask for;
// the card and its dialog are shot as a docker hub's, with a Claude token
// once the harness says so.
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

// The Harness card's sign-in dialog (#929), opened over the first-run page.
await page.route('**/api/settings', dockerSettings(false))
await page.route('**/api/onboarding', (route) => route.fulfill({ json: firstRun.none }))
await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.locator('.pillar').first().locator('.pillar-actions .btn').first().click()
await page.locator('.step-dialog').waitFor({ timeout: 15000 })
const signInDialogFile = `${outDir}/first-run-sign-in-dialog.png`
await page.screenshot({ path: signInDialogFile })
taken.push(signInDialogFile)
await page.unroute('**/api/settings')

// Settings → Claude logins (#929, #1070) after the hub's login check
// (#588) refused the default, which its one line reads.
await page.route('**/api/claude-logins', (route) =>
  route.fulfill({
    json: {
      logins: [
        {
          name: 'default',
          signed_in: true,
          default: true,
          created_at: 0,
          check: { status: 'refused', refusal: 'OAuth token has expired', at: Date.now() - 120000 },
        },
      ],
    },
  }),
)
const cloudView = await (await page.request.get(base + '/api/settings/cloud')).json()
await page.route('**/api/settings/cloud', (route) =>
  route.fulfill({
    json: {
      ...cloudView,
      account: { set: true, cli: true, check: { state: 'ok', checked_at: Date.now() - 720000 } },
    },
  }),
)
await page.goto(base + '/settings', { waitUntil: 'networkidle' })
const claudeLogin = page.locator('section.claude-login')
await claudeLogin.locator('.cloud-step.bad').waitFor({ timeout: 15000 })
const loginCheckFile = `${outDir}/settings-login-check.png`
await claudeLogin.screenshot({ path: loginCheckFile })
taken.push(loginCheckFile)
await page.unroute('**/api/claude-logins')
await page.unroute('**/api/settings/cloud')

// The Loops and Chat surface cards' dialogs (#589), opened over the
// first-run page. The fixture's loops all carry Telegram bots; the attach
// shot strips them so the dialog offers the surface choice.
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

// The pair step is a link (#775): the operator links their own account
// through the loop's bot, since pairing codes are gone (ADR-0051). Shot
// in a token session first, which is no user and can link nothing; then,
// as admin, the owner signed in with a password: before a code, with one
// open, and on a Slack loop once linked, where a user whose role is owner
// is made its owner at once (#792). The code is answered here so the shot
// shows a fixed, synthetic one.
const now = Date.now()
const surfaceStep = page.locator('.surface-dialog')
const openPairStep = async () => {
  await page.goto(base + '/', { waitUntil: 'networkidle' })
  await page.getByRole('button', { name: 'Attach a bot' }).click()
  await surfaceStep.waitFor({ timeout: 15000 })
}
await page.route('**/api/me', (route) =>
  route.fulfill({ json: { name: '', role: 'owner', must_change_password: false, via: 'token' } }),
)
await openPairStep()
await surfaceStep.getByText('A link belongs to a user').waitFor({ timeout: 15000 })
const surfaceTokenFile = `${outDir}/first-run-surface-token.png`
await page.screenshot({ path: surfaceTokenFile })
taken.push(surfaceTokenFile)
await page.unroute('**/api/me')

await openPairStep()
await surfaceStep.getByRole('button', { name: 'Get a link code' }).waitFor({ timeout: 15000 })
const surfaceLinkFile = `${outDir}/first-run-surface-link.png`
await page.screenshot({ path: surfaceLinkFile })
taken.push(surfaceLinkFile)

await page.route('**/api/me/links/code', (route) =>
  route.fulfill({ json: { code: 'link-fixture0002', expires_at: now + 15 * 60 * 1000 } }),
)
await surfaceStep.getByRole('button', { name: 'Get a link code' }).click()
await surfaceStep.locator('.otp-value').waitFor({ timeout: 15000 })
const surfaceCodeFile = `${outDir}/first-run-surface-code.png`
await page.screenshot({ path: surfaceCodeFile })
taken.push(surfaceCodeFile)
await page.unroute('**/api/me/links/code')

// Signed in as the owner admin, the dialog makes them the Slack loop's
// owner itself.
let slackOwned = false
const firstSlackLoop = (l) => ({
  ...l,
  surface: 'slack',
  has_tg_token: false,
  tg_bot_username: '',
  has_slack_tokens: true,
  owner_tg_user_id: undefined,
  owner_slack_user_id: slackOwned ? 'U0FIXTURE1' : undefined,
  owner_user: slackOwned ? 'admin' : '',
})
await page.route('**/api/loops', async (route) => {
  const loops = await (await route.fetch()).json()
  await route.fulfill({ json: loops.map(firstSlackLoop) })
})
await page.route('**/api/loops/*/owner', async (route) => {
  slackOwned = true
  const loop = await (
    await route.fetch({
      method: 'GET',
      url: route
        .request()
        .url()
        .replace(/\/owner$/, ''),
    })
  ).json()
  await route.fulfill({ json: firstSlackLoop(loop) })
})
await page.unroute('**/api/onboarding')
await page.route('**/api/onboarding', (route) =>
  route.fulfill({
    json: {
      ...firstRun.two,
      surface: { done: false, reason: 'no message received from the operator on slack yet' },
    },
  }),
)
await page.route('**/api/me/links', (route) =>
  route.fulfill({
    json: [
      { surface: 'slack', id: 'U0FIXTURE1', username: 'operator', display: 'Operator', created_at: now },
    ],
  }),
)
await page.route('**/api/slack/senders', (route) =>
  route.fulfill({
    json: [
      {
        slack_user_id: 'U0FIXTURE1',
        team_id: 'T0FIXTURE',
        username: 'operator',
        display: 'Operator',
        status: 'allowed',
        user: 'admin',
        pair_code: '',
        first_seen_via: 'gardener',
        created_at: now,
        updated_at: now,
      },
    ],
  }),
)
await openPairStep()
await surfaceStep.getByText('You are its owner.').waitFor({ timeout: 15000 })
const surfaceOwnerFile = `${outDir}/first-run-surface-owner.png`
await page.screenshot({ path: surfaceOwnerFile })
taken.push(surfaceOwnerFile)
await page.unroute('**/api/slack/senders')
await page.unroute('**/api/me/links')
await page.unroute('**/api/loops/*/owner')
await page.unroute('**/api/loops')
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

// The owner picker (#732): an owner is a user, by their link on the loop's
// surface. The fixture's gardener is owned by rana, allowed before links and
// linked as nobody, so the picker shows them greyed as not linked and offers
// sam, linked on Telegram. A native select draws its list outside the page,
// so the shot opens it as a list box to show what is offered.
const ownerField = page.locator('.field:has(#owner-select)')
const shootOwner = async (name) => {
  await page.goto(base + '/loops/gardener', { waitUntil: 'networkidle' })
  await ownerField.locator('option').first().waitFor({ state: 'attached', timeout: 15000 })
  await page.locator('#owner-select').evaluate((select) => {
    select.size = select.options.length
  })
  const file = `${outDir}/${name}.png`
  await ownerField.screenshot({ path: file })
  taken.push(file)
}
await shootOwner('owner-not-linked')
// And once sam owns it, as the view says after the picker sets them.
await page.route('**/api/loops/gardener', async (route) => {
  if (route.request().method() !== 'GET') return route.continue()
  const loop = await (await route.fetch()).json()
  await route.fulfill({
    json: {
      ...loop,
      owner_tg_user_id: 700000004,
      owner_username: 'sam_example',
      owner_user: 'sam',
      owner_dm_ready: true,
    },
  })
})
await shootOwner('owner-linked')
await page.unroute('**/api/loops/gardener')

await browser.close()
for (const file of taken) console.log(file)
