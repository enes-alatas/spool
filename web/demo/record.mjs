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
const token = readFileSync(process.env.SPOOL_DATA_DIR + '/operator-token', 'utf8').trim()
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
const fontCSS = [
  ...[400, 500, 600, 700].map((w) => face('Inter', '@fontsource/inter', `inter-latin-${w}-normal.woff2`, w)),
  ...[400, 500, 700].map((w) =>
    face('JetBrains Mono', '@fontsource/jetbrains-mono', `jetbrains-mono-latin-${w}-normal.woff2`, w),
  ),
  ":root{--sans:'Inter',sans-serif !important;--mono:'JetBrains Mono',monospace !important;font-feature-settings:'cv11','ss01'}",
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
    document.documentElement.append(ring, c)
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
if (await lp.isVisible('#operator-token')) {
  await lp.fill('#operator-token', token)
  await lp.click('button[type=submit]')
  await lp.waitForSelector('.topbar')
}
const storageState = await login.storageState()
await login.close()

// take records one clip. The clock starts when fn is called; mark(label)
// notes the clip-relative second of a moment, and every mark lands in
// public/marks.json, which SpoolDemo.tsx cuts and pans on.
const marks = {}
async function take(name, fn) {
  const ctx = await browser.newContext({ viewport: size, deviceScaleFactor: DPR, storageState })
  await ctx.addInitScript(injectFonts, fontCSS)
  await ctx.addInitScript(injectCursor)
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
  const mark = (label) => (marks[name][label] = +(Date.now() / 1000 - t0).toFixed(2))
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

await take('fleet', async (page, mark) => {
  await page.goto(base + '/', { waitUntil: 'networkidle' })
  await page.waitForSelector('.fleet-row')
  mark('ready')
  await page.waitForTimeout(6000)
})

await take('create', async (page, mark) => {
  let attached = false
  const bound = (l) => ({
    ...l,
    surface: 'telegram',
    has_tg_token: true,
    tg_bot_username: 'scout_example_bot',
  })
  await page.route('**/api/loops/scout', async (route) => {
    const req = route.request()
    if (req.method() === 'PATCH') {
      attached = true
      const res = await route.fetch({ method: 'GET' })
      return route.fulfill({ json: bound(await res.json()) })
    }
    if (!attached) return route.continue()
    const res = await route.fetch()
    return route.fulfill({ response: res, json: bound(await res.json()) })
  })
  await page.goto(base + '/new', { waitUntil: 'networkidle' })
  await page.waitForSelector('#nl-runtime')
  await page.mouse.move(size.width / 2, size.height - 80)
  mark('ready')
  await page.waitForTimeout(700)
  await pointAt(page, page.locator('#nl-name'))
  await page.keyboard.type('scout', { delay: 110 })
  await page.waitForTimeout(300)
  await pointAt(page, page.locator('#nl-mission'))
  mark('mission')
  await page.keyboard.type('Keep main green: watch CI, find what broke, and tell me in plain words.', {
    delay: 32,
  })
  await page.waitForTimeout(600)
  const create = page.getByRole('button', { name: 'Create loop' })
  mark('scroll-create')
  await glideTo(page, create)
  mark('create-click')
  await pointAt(page, create)
  await page.waitForURL('**/loops/scout')
  mark('loop-page')
  await page.waitForTimeout(1000)
  const surfaces = page.locator('.side-panel').filter({ has: page.locator('h3', { hasText: /^Surfaces?$/ }) })
  mark('scroll-surfaces')
  await glideTo(page, surfaces)
  mark('attach-click')
  await pointAt(page, page.getByRole('button', { name: 'Attach Telegram' }))
  await page.waitForTimeout(300)
  const tokenInput = surfaces.locator('input').first()
  await pointAt(page, tokenInput)
  await page.keyboard.type('123456789:synthetic-demo-token', { delay: 30 })
  await page.waitForTimeout(400)
  mark('save-click')
  await pointAt(page, surfaces.getByRole('button', { name: /^(Attach|Save)/ }).first())
  mark('attached')
  await page.waitForTimeout(3000)
  mark('end')
})

await take('talk', async (page, mark) => {
  await page.goto(base + '/loops/gardener', { waitUntil: 'networkidle' })
  await page.waitForSelector('.timeline')
  mark('ready')
  await page.waitForTimeout(8000)
})

await take('channel', async (page, mark) => {
  await page.goto(base + '/?view=channel', { waitUntil: 'networkidle' })
  await page.waitForSelector('.knot')
  mark('ready')
  await page.waitForTimeout(16000)
})

await browser.close()
writeFileSync(out + 'marks.json', JSON.stringify(marks, null, 2) + '\n')
console.log(marks)
