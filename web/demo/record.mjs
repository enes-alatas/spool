// Records control-room footage from the fixture hub (never the live fleet)
// for the README demo. Run via scripts/fixture-hub.sh.
//
// Playwright's own recordVideo encodes VP8 at a fixed 1 Mbps, which smears
// text; instead each clip is a run of 2x device-pixel screenshots, assembled
// into a near-lossless H.264 file with Remotion's bundled ffmpeg.
import { chromium } from 'playwright'
import { execFileSync } from 'node:child_process'
import { readFileSync, readdirSync, writeFileSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const require = createRequire(import.meta.url)
const base = process.env.SPOOL_URL
const password = readFileSync(process.env.SPOOL_DATA_DIR + '/fixture-user-password', 'utf8').trim()
const out = new URL('./public/', import.meta.url).pathname
mkdirSync(out, { recursive: true })
const size = { width: 1280, height: 720 }
const DPR = 2
const FPS = 30

// Remotion installs the compositors for the host's platform (on Linux both
// glibc and musl); the first serves, as in render.sh.
const remotion = new URL('./node_modules/@remotion/', import.meta.url).pathname
const compositor = join(
  remotion,
  readdirSync(remotion)
    .sort()
    .find((d) => d.startsWith('compositor-')),
)
const ffmpeg = (args) =>
  execFileSync(join(compositor, 'ffmpeg'), ['-hide_banner', '-loglevel', 'error', ...args], {
    env: { ...process.env, LD_LIBRARY_PATH: compositor },
    stdio: 'inherit',
  })

// The control room names system fonts; a bare Linux box resolves them to
// DejaVu. Pin the same faces the composition uses.
const face = (family, pkg, file, weight) => {
  const b64 = readFileSync(require.resolve(`${pkg}/files/${file}`)).toString('base64')
  return `@font-face{font-family:'${family}';font-weight:${weight};font-style:normal;src:url(data:font/woff2;base64,${b64}) format('woff2')}`
}
// Nor does it have an emoji face, and a reaction chip draws an empty box
// without one. This build of Noto Color Emoji is COLRv1, which Chromium
// paints; fontsource's build carries the colour only as an SVG table,
// which Chromium does not, and its glyphs came out blank. The subsets go
// by unicode-range and are served from disk on the hub's own origin (see
// take) rather than inlined into every page.
const EMOJI_PATH = '/__demo-emoji/'
const emojiDir = join(require.resolve('@infolektuell/noto-color-emoji/package.json'), '../files/')
function emojiCSS() {
  const css = readFileSync(join(emojiDir, '../index.css'), 'utf8')
  return css.replace(/url\(\.\/files\/([^)]+\.woff2)\)/g, (_, file) => `url(${EMOJI_PATH}${file})`)
}
const fontCSS = [
  ...[400, 500, 600, 700].map((w) => face('Inter', '@fontsource/inter', `inter-latin-${w}-normal.woff2`, w)),
  ...[400, 500, 700].map((w) =>
    face('JetBrains Mono', '@fontsource/jetbrains-mono', `jetbrains-mono-latin-${w}-normal.woff2`, w),
  ),
  emojiCSS(),
  ":root{--sans:'Inter','Noto Color Emoji',sans-serif !important;--mono:'JetBrains Mono','Noto Color Emoji',monospace !important;font-feature-settings:'cv11','ss01'}",
].join('\n')
const injectFonts = (css) => {
  const add = () => {
    const el = document.createElement('style')
    el.textContent = css
    document.head.appendChild(el)
  }
  if (document.head) add()
  else document.addEventListener('DOMContentLoaded', add)
}

// Headless Chromium draws no pointer, so a click would land with nothing on
// screen moving toward it. Draw one: an arrow that follows the mouse and a
// ring that pulses where it presses.
const injectCursor = () => {
  const add = () => {
    const c = document.createElement('div')
    c.innerHTML =
      '<svg width="22" height="26" viewBox="0 0 22 26"><path d="M2 2 L2 21 L7 16.5 L10.5 24 L14 22.5 L10.6 15 L17 15 Z" fill="#f2f1ed" stroke="#050505" stroke-width="1.6" stroke-linejoin="round"/></svg>'
    Object.assign(c.style, {
      position: 'fixed',
      left: '-40px',
      top: '-40px',
      zIndex: 2147483647,
      pointerEvents: 'none',
    })
    const ring = document.createElement('div')
    Object.assign(ring.style, {
      position: 'fixed',
      width: '34px',
      height: '34px',
      margin: '-17px 0 0 -17px',
      borderRadius: '50%',
      border: '2px solid rgba(242,241,237,0.8)',
      opacity: 0,
      zIndex: 2147483646,
      pointerEvents: 'none',
      transition: 'opacity 0.5s, transform 0.5s',
    })
    // A modal dialog sits in the top layer, above any z-index, so the
    // pointer is a popover too, raised again over each dialog that opens.
    for (const el of [ring, c]) {
      el.popover = 'manual'
      Object.assign(el.style, {
        right: 'auto',
        bottom: 'auto',
        padding: 0,
        overflow: 'visible',
        background: 'transparent',
      })
    }
    // The ring keeps its own border and the margin that centres it.
    Object.assign(c.style, { margin: 0, border: 'none' })
    document.documentElement.append(ring, c)
    const raise = () => {
      for (const el of [ring, c]) {
        if (el.matches(':popover-open')) el.hidePopover()
        el.showPopover()
      }
    }
    raise()
    const showModal = window.HTMLDialogElement.prototype.showModal
    window.HTMLDialogElement.prototype.showModal = function () {
      showModal.call(this)
      raise()
    }
    addEventListener(
      'mousemove',
      (e) => Object.assign(c.style, { left: e.clientX - 2 + 'px', top: e.clientY - 2 + 'px' }),
      true,
    )
    addEventListener(
      'mousedown',
      (e) => {
        Object.assign(ring.style, {
          left: e.clientX + 'px',
          top: e.clientY + 'px',
          transition: 'none',
          opacity: 1,
          transform: 'scale(0.4)',
        })
        requestAnimationFrame(() =>
          Object.assign(ring.style, {
            transition: 'opacity 0.5s, transform 0.5s',
            opacity: 0,
            transform: 'scale(1)',
          }),
        )
      },
      true,
    )
  }
  if (document.documentElement) add()
  else addEventListener('DOMContentLoaded', add)
}

// The newest shown message in view, and the composer under it: the thread
// scrolls itself on Fleet, and the whole page scrolls on a loop's.
const showNewest = () => {
  window.__showNewest = (behavior) => {
    const shown = [...document.querySelectorAll('.knot')].filter((k) => k.style.display !== 'none')
    shown.at(-1)?.scrollIntoView({ block: 'nearest', behavior })
    document.querySelector('.composer')?.scrollIntoView({ block: 'end', behavior })
  }
}

// Move the pointer to an element at a human pace, settle, then click it.
async function pointAt(page, locator) {
  const box = await locator.boundingBox()
  await page.mouse.move(box.x + Math.min(box.width / 2, 60), box.y + box.height / 2, { steps: 28 })
  await page.waitForTimeout(350)
  await page.mouse.down()
  await page.mouse.up()
  await page.waitForTimeout(250)
}

// Scroll an element into the middle of the viewport the way a wheel would,
// not in one jump.
async function glideTo(page, locator) {
  await locator.evaluate((el) => el.scrollIntoView({ behavior: 'smooth', block: 'center' }))
  await page.waitForTimeout(1400)
}

const browser = await chromium.launch()

const login = await browser.newContext({ viewport: size })
const lp = await login.newPage()
await lp.goto(base + '/', { waitUntil: 'networkidle' })
if (await lp.isVisible('#username')) {
  await lp.fill('#username', 'admin')
  await lp.fill('#password', password)
  await lp.click('button[type=submit]')
  await lp.waitForSelector('.topbar')
}
const storageState = await login.storageState()
await login.close()

// take records one clip. The clock starts when fn is called; mark(label)
// notes the clip-relative second of a moment, and every mark lands in
// public/marks.json, which SpoolDemo.tsx cuts and pans on.
// times holds the clock a message shows in the room, by clip and mark, so
// the chat pane beside it stamps its bubble with the same minute.
// DEMO_TAKES, a comma-separated list such as "cloud", records only those
// clips and keeps every other clip's marks from the last run. The link clip
// needs the loop the create clip makes, so those two go together.
const only = process.env.DEMO_TAKES ? process.env.DEMO_TAKES.split(',') : null
const { times: lastTimes = {}, ...lastMarks } = only
  ? JSON.parse(readFileSync(out + 'marks.json', 'utf8'))
  : {}
const marks = lastMarks
const times = lastTimes
async function take(name, fn, viewport = size) {
  if (only && !only.includes(name)) return
  const ctx = await browser.newContext({ viewport, deviceScaleFactor: DPR, storageState })
  await ctx.addInitScript(injectFonts, fontCSS)
  await ctx.route(`**${EMOJI_PATH}*`, (route) =>
    route.fulfill({
      contentType: 'font/woff2',
      body: readFileSync(join(emojiDir, new URL(route.request().url()).pathname.slice(EMOJI_PATH.length))),
    }),
  )
  await ctx.addInitScript(injectCursor)
  await ctx.addInitScript(showNewest)
  const page = await ctx.newPage()
  await page.goto('about:blank')
  // A CDP screencast (or a raw CDP screenshot from a fresh session) comes
  // back at CSS pixels whatever the scale factor; Playwright's own
  // screenshot honours it, so grab those back to back.
  const frames = []
  let recording = true
  const grabbing = (async () => {
    while (recording) {
      const t = Date.now() / 1000
      const data = await page
        .screenshot({ type: 'jpeg', quality: 96, animations: 'allow', caret: 'initial' })
        .catch(() => null)
      if (data) frames.push({ data, t })
    }
  })()
  const t0 = Date.now() / 1000
  marks[name] = {}
  times[name] = {}
  const mark = (label) => (marks[name][label] = +(Date.now() / 1000 - t0).toFixed(2))
  mark.time = async (label, snippet) => (times[name][label] = await timeOf(page, snippet))
  await fn(page, mark)
  const tEnd = Date.now() / 1000
  recording = false
  await grabbing
  await ctx.close()

  // Drop the about:blank frames, then hold each frame until the next one
  // arrives: the concat demuxer's per-file durations give a variable-rate
  // stream that ffmpeg resamples to a constant FPS.
  const shown = frames.filter((f) => f.t >= t0)
  const dir = mkdtempSync(join(tmpdir(), `spool-demo-${name}-`))
  let list = ''
  shown.forEach((f, i) => {
    const file = join(dir, `${String(i).padStart(5, '0')}.jpg`)
    writeFileSync(file, f.data)
    const next = i + 1 < shown.length ? shown[i + 1].t : tEnd
    list += `file '${file}'\nduration ${Math.max(next - f.t, 0.001).toFixed(4)}\n`
  })
  list += `file '${join(dir, `${String(shown.length - 1).padStart(5, '0')}.jpg`)}'\n`
  writeFileSync(join(dir, 'list.txt'), list)
  ffmpeg([
    '-y',
    '-f',
    'concat',
    '-safe',
    '0',
    '-i',
    join(dir, 'list.txt'),
    '-fps_mode',
    'cfr',
    '-r',
    String(FPS),
    '-c:v',
    'libx264',
    '-preset',
    'slow',
    '-crf',
    '10',
    '-pix_fmt',
    'yuv420p',
    out + name + '.mp4',
  ])
  rmSync(dir, { recursive: true, force: true })
  console.log('recorded', name, `${shown.length} frames, ${(tEnd - t0).toFixed(1)}s`)
}

// The first-run Loops card (#733): its New loop dialog creates scout, and
// the dialog stays to tick its first wake. The fixture hub has done its
// first run, so the onboarding answers stand in for one that has not, and
// the wake ticks on cue. The loop itself is created for real: the link
// clip gives it a bot.
await take('create', async (page, mark) => {
  let woken = false
  await page.route('**/api/onboarding', (route) =>
    route.fulfill({
      json: {
        completed: false,
        harness: { done: true, reason: 'a turn authenticated' },
        surface: { done: false, reason: 'no chat surface has carried a message yet' },
        loops: woken ? { done: true } : { done: false, reason: 'no loops' },
      },
    }),
  )
  await page.goto(base + '/', { waitUntil: 'networkidle' })
  await page.waitForSelector('.pillars')
  await page.mouse.move(size.width / 2, size.height - 80)
  mark('ready')
  await page.waitForTimeout(900)
  await pointAt(page, page.locator('.pillar').getByRole('button', { name: 'New loop', exact: true }))
  const dialog = page.locator('.loop-dialog')
  await dialog.locator('#nl-name').waitFor()
  mark('dialog')
  await page.waitForTimeout(500)
  await pointAt(page, dialog.locator('#nl-name'))
  await page.keyboard.type('scout', { delay: 110 })
  await page.waitForTimeout(300)
  await pointAt(page, dialog.locator('#nl-mission'))
  mark('mission')
  await page.keyboard.type('Keep main green: watch CI, find what broke, and tell me in plain words.', {
    delay: 32,
  })
  await page.waitForTimeout(600)
  const create = dialog.getByRole('button', { name: 'Create loop' })
  mark('scroll-create')
  await glideTo(page, create)
  mark('create-click')
  await pointAt(page, create)
  await dialog.getByText('Created @scout').waitFor()
  mark('created')
  await page.waitForTimeout(1800)
  woken = true
  await dialog.getByText('@scout has woken.').waitFor()
  mark('woken')
  await page.waitForTimeout(1200)
  // Done closes the dialog, and the Loops card's tick draws in.
  await pointAt(page, dialog.getByRole('button', { name: 'Done' }))
  mark('done')
  await page.waitForTimeout(2000)
  mark('end')
})

// The first-run Chat surface card (#733): attach scout's bot, get a link
// code, and see the link and the ownership land. The fixture hub has done
// its first run and has no Telegram, so its answers stand in for one that
// has not: the composition's chat pane plays the operator's side, and the
// phases below follow its clock. Only the message at the end is real: the
// operator writes to scout again, and its event refreshes the fleet list,
// which is how the dialog learns that the link made them scout's owner.
await take('link', async (page, mark) => {
  const now = Date.now()
  let attached = false
  let phase = 'none' // waiting → linked → owned
  const tgID = 4242001
  const view = (l) => {
    if (l.name !== 'scout') return l
    const bot = attached
      ? { surface: 'telegram', has_tg_token: true, tg_bot_username: 'scout_example_bot' }
      : {}
    const owner = phase === 'owned' ? { owner_tg_user_id: tgID, owner_user: 'admin' } : {}
    return { ...l, surface: '', has_tg_token: false, tg_bot_username: '', ...bot, ...owner }
  }
  await page.route('**/api/onboarding', (route) =>
    route.fulfill({
      json: {
        completed: false,
        harness: { done: true, reason: 'a turn authenticated' },
        surface: { done: false, reason: 'no message received from the operator on telegram yet' },
        loops: { done: true },
      },
    }),
  )
  // A first run has the one loop it just created, so the dialog opens on
  // scout rather than on a fixture loop that already has its bot.
  await page.route('**/api/loops', async (route) => {
    const loops = await (await route.fetch()).json()
    await route.fulfill({ json: loops.filter((l) => l.name === 'scout').map(view) })
  })
  await page.route('**/api/loops/scout', async (route) => {
    if (route.request().method() === 'PATCH') attached = true
    const res = await route.fetch({ method: 'GET' })
    return route.fulfill({ json: view(await res.json()) })
  })
  await page.route('**/api/me/links/code', (route) =>
    route.fulfill({ json: { code: 'link-demo4821', expires_at: now + 15 * 60 * 1000 } }),
  )
  const person = { surface: 'telegram', id: String(tgID), username: 'operator', display: 'Operator' }
  await page.route('**/api/me/links/waiting', (route) =>
    route.fulfill({
      json: phase === 'waiting' ? [{ ...person, loop: 'scout', expires_at: now + 15 * 60 * 1000 }] : [],
    }),
  )
  await page.route('**/api/me/links', (route) =>
    route.fulfill({ json: phase === 'linked' || phase === 'owned' ? [{ ...person, created_at: now }] : [] }),
  )

  await page.goto(base + '/', { waitUntil: 'networkidle' })
  await page.waitForSelector('.pillars')
  await page.mouse.move(size.width / 2, size.height - 80)
  mark('ready')
  await page.waitForTimeout(900)
  await pointAt(page, page.getByRole('button', { name: 'Attach a bot' }))
  const dialog = page.locator('.surface-dialog')
  await dialog.waitFor()
  await page.waitForTimeout(700)
  mark('attach-click')
  await pointAt(page, dialog.getByRole('button', { name: 'Attach Telegram' }))
  await page.waitForTimeout(300)
  await pointAt(page, dialog.locator('input[type=password]'))
  await page.keyboard.type('123456789:synthetic-demo-token', { delay: 30 })
  await page.waitForTimeout(400)
  await pointAt(page, dialog.getByRole('button', { name: 'Save' }))
  await dialog.getByRole('button', { name: 'Get a link code' }).waitFor()
  mark('attached')
  await page.waitForTimeout(1200)
  await pointAt(page, dialog.getByRole('button', { name: 'Get a link code' }))
  await dialog.locator('.otp-value').waitFor()
  mark('code')
  await page.waitForTimeout(2500)
  phase = 'waiting'
  mark('sent')
  await dialog.getByText('Reply YES there').waitFor()
  await page.waitForTimeout(2500)
  phase = 'linked'
  mark('yes')
  await dialog.getByText('Linked:').waitFor()
  await page.waitForTimeout(2000)
  phase = 'owned'
  mark('again')
  const said = await page.request.post(base + '/api/loops/scout/message', {
    data: { author: 'operator', text: 'hi scout' },
  })
  if (!said.ok()) throw new Error(`link: the message to scout was refused: ${said.status()}`)
  await dialog.getByText('You are its owner.').waitFor()
  mark('owner')
  await page.waitForTimeout(3000)
  mark('end')
})

// The fleet, after the link, so scout is in it.
await take('fleet', async (page, mark) => {
  await page.goto(base + '/', { waitUntil: 'networkidle' })
  await page.waitForSelector('.fleet-row')
  mark('ready')
  await page.waitForTimeout(6000)
})

// A thread staged to run live: the messages a chat pane will show arriving
// are hidden when the clip starts, and each comes back on cue, so the room
// and the drawn chat beside it move together. The fixture holds the
// messages already; this only decides when they appear.
const hide = (page, snippets) =>
  page.evaluate((snippets) => {
    for (const knot of document.querySelectorAll('.knot')) {
      if (snippets.some((t) => knot.textContent.includes(t))) knot.style.display = 'none'
    }
    window.__showNewest('instant')
  }, snippets)
// Show or hide the reactions under the message holding snippet.
const setReactions = (page, snippet, display) =>
  page.evaluate(
    ([snippet, display]) => {
      for (const knot of document.querySelectorAll('.knot')) {
        if (!knot.textContent.includes(snippet)) continue
        for (const r of knot.querySelectorAll('.reactions')) r.style.display = display
      }
    },
    [snippet, display],
  )
// The clock on the message holding snippet, as its header shows it.
const timeOf = (page, snippet) =>
  page.evaluate((snippet) => {
    const knot = [...document.querySelectorAll('.knot')].find((k) => k.textContent.includes(snippet))
    return knot?.querySelector('.who')?.textContent.match(/\d\d:\d\d\s?[AP]M/)?.[0] ?? ''
  }, snippet)
const reveal = (page, snippet) =>
  page.evaluate((snippet) => {
    for (const knot of document.querySelectorAll('.knot')) {
      if (knot.textContent.includes(snippet)) knot.style.display = ''
    }
    window.__showNewest('smooth')
  }, snippet)
// The fleet channel, beside the Telegram group that carries it: rana asks
// with a screenshot, gardener answers, and the group reacts. The clip is
// recorded at the size of the window it plays in, so the composition shows
// all of it at 1:1 rather than cropping a 16:9 page.
const ASK = 'the install page still tells people'
const ANSWER = 'Three pages, all fixed'
await take(
  'talk',
  async (page, mark) => {
    await page.goto(base + '/?view=channel', { waitUntil: 'networkidle' })
    await page.waitForSelector('.knot')
    // The photo rana sends in Telegram is the one the room shows her post
    // carrying.
    const shot = page.locator('.knot', { hasText: ASK }).locator('img').first()
    await shot.scrollIntoViewIfNeeded()
    await shot.screenshot({ path: out + 'install-page.png' })
    await mark.time('ask', ASK)
    await mark.time('answer', ANSWER)
    await hide(page, [ASK, ANSWER, 'the handbook freeze'])
    await setReactions(page, ANSWER, 'none')
    await page.waitForTimeout(400)
    mark('ready')
    await page.waitForTimeout(1000)
    await reveal(page, ASK)
    mark('ask')
    await page.waitForTimeout(3600)
    await reveal(page, ANSWER)
    mark('answer')
    await page.waitForTimeout(1800)
    await setReactions(page, ANSWER, '')
    mark('reactions')
    await page.waitForTimeout(3000)
  },
  { width: 1098, height: 750 },
)

// A channel of two loops and the operator, docs, on one of the loops'
// pages, beside the Telegram group that carries it.
const DOCS = [
  'the incident write-up template links the old runbook',
  'Mine, it moved with the handbook',
  'once that lands, close the stale-runbook issue',
]
await take('docs', async (page, mark) => {
  // The fixture's archivist has no surface, and the operator's post in docs
  // came from the room. The scene is two bots and their owner in a Telegram
  // group carrying docs, so the archivist gets a bot and a room, as scout
  // gets one in the link clip, and the operator's post comes in through
  // Telegram.
  const withBot = (l) =>
    l.name === 'archivist'
      ? { ...l, surface: 'telegram', has_tg_token: true, tg_bot_username: 'archivist_example_bot' }
      : l
  await page.route('**/api/loops', async (route) => {
    const res = await route.fetch()
    return route.fulfill({ response: res, json: (await res.json()).map(withBot) })
  })
  await page.route('**/api/loops/archivist', async (route) => {
    const res = await route.fetch()
    return route.fulfill({ response: res, json: withBot(await res.json()) })
  })
  await page.route('**/api/loops/archivist/rooms', (route) =>
    route.fulfill({
      json: [
        {
          surface: 'telegram',
          room_id: '-1002000000002',
          title: 'Docs reviewers',
          channel: 'docs',
          first_seen_at: Date.now() - 864e5,
          bound_at: Date.now() - 864e5,
        },
      ],
    }),
  )
  await page.route('**/api/channels/docs/messages*', async (route) => {
    if (route.request().method() !== 'GET') return route.continue()
    const res = await route.fetch()
    // The exchange runs as one conversation, a minute apart, as the
    // Telegram group beside it shows it.
    const fromTelegram = (m) =>
      m.origin === 'web' ? { ...m, origin: 'telegram-group', mirror: 'mirrored' } : m
    const minute = (m) => {
      const i = DOCS.findIndex((t) => m.text.includes(t))
      return i === -1 ? m : { ...m, ts: Date.now() - (DOCS.length - i) * 60_000 }
    }
    return route.fulfill({ response: res, json: (await res.json()).map((m) => minute(fromTelegram(m))) })
  })
  await page.goto(base + '/loops/gardener?channel=docs', { waitUntil: 'networkidle' })
  await page.waitForSelector('.knot')
  for (const [i, snippet] of DOCS.entries()) await mark.time(`line${i}`, snippet)
  await hide(page, [...DOCS, 'which write-ups should move'])
  await page.waitForTimeout(400)
  mark('ready')
  for (const [i, snippet] of DOCS.entries()) {
    await page.waitForTimeout(i === 0 ? 800 : 2400)
    await reveal(page, snippet)
    mark(`line${i}`)
  }
  await page.waitForTimeout(3000)
})

// The report beat: watcher's own timeline, beside the Telegram DM it opens
// with its owner. The room does not draw an owner DM's text, so what shows
// here is the loop's side of it: the tick, its reasoning and the
// send_message call that asks, the owner's answer arriving, and the call
// that reacts to it. The fixture's watcher has no transcript, so the turn is
// served in place of its events.
const REPORT = {
  reason: 'Reverting is the owner',
  reply: 'Yes, revert at 09:00',
}
await take(
  'report',
  async (page, mark) => {
    const now = Date.now()
    const at = (sec) => now - 90_000 + sec * 1000
    const utc = new Date(now).toISOString().slice(0, 16).replace('T', ' ')
    const event = (id, sec, type, payload, turn) => ({
      id,
      loop_id: 'watcher',
      session_id: 'demo-session',
      turn_id: turn,
      ts: at(sec),
      type,
      subtype: '',
      payload: JSON.stringify(payload),
    })
    const assistant = (content) => ({ message: { content } })
    const send = (id, input) => ({ type: 'tool_use', name: 'mcp__spool__send_message', id, input })
    const result = (ms, cost, inTok, outTok) => ({
      total_cost_usd: cost,
      duration_ms: ms,
      is_error: false,
      usage: { input_tokens: inTok, output_tokens: outTok },
    })
    const events = [
      event(
        990001,
        0,
        'envelope',
        { trigger: 'tick', text: `[tick · ${utc} UTC · fixed]\n\nContinue working toward your mission.` },
        't1',
      ),
      event(
        990002,
        6,
        'assistant',
        assistant([
          {
            type: 'text',
            text: "Nightly broke again at the same fixture. The change it belongs to is still open and the operator is chasing its author. Reverting is the owner's call, so I am asking rather than reverting.",
          },
          send('toolu_demo_1', { destination: 'owner_dm', text: 'Nightly broke again at the same fixture…' }),
        ]),
        't1',
      ),
      event(990003, 7, 'result', result(7400, 0.0131, 5120, 214), 't1'),
      event(
        990004,
        40,
        'envelope',
        {
          trigger: 'message',
          text: `[message from @operator via telegram · owner_dm · ref:212 · ${utc} UTC]\n\nYes, revert at 09:00 if it is still red.`,
        },
        't2',
      ),
      event(
        990005,
        43,
        'assistant',
        assistant([send('toolu_demo_2', { destination: 'owner_dm', react: '👍', reply_to: 'ref:212' })]),
        't2',
      ),
      event(990006, 44, 'result', result(3100, 0.0174, 5390, 41), 't2'),
    ]
    await page.route('**/api/loops/watcher/events*', (route) => route.fulfill({ json: events }))
    await page.goto(base + '/loops/watcher', { waitUntil: 'networkidle' })
    await page.waitForSelector('.knot')
    const show = (ids, display) =>
      page.evaluate(
        ([ids, display]) => {
          for (const id of ids) {
            const knot = document.querySelector(`.knot[data-entry-id="${id}"]`)
            if (knot) knot.style.display = display
          }
        },
        [ids, display],
      )
    await mark.time('ask', REPORT.reason)
    await mark.time('reply', REPORT.reply)
    await show([990002, 990003, 990004, 990005, 990006], 'none')
    await page.waitForTimeout(400)
    mark('ready')
    await page.waitForTimeout(1600)
    await show([990002, 990003], '')
    mark('ask')
    await page.waitForTimeout(2800)
    await show([990004], '')
    mark('reply')
    await page.waitForTimeout(1200)
    await show([990005, 990006], '')
    mark('react')
    await page.waitForTimeout(3000)
  },
  { width: 1098, height: 750 },
)

await browser.close()
writeFileSync(out + 'marks.json', JSON.stringify({ ...marks, times }, null, 2) + '\n')
console.log(marks)
