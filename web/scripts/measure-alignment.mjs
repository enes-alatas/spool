// Measures the control room's alignment on a fixture hub and reports every
// offset over half a pixel (#1549). Offsets of a pixel or two reached main
// twice, a letter sitting high in a round icon (#1546) and a field beside a
// button at another height (#1547): a 1× screenshot cannot show them, and
// review had nothing that measured.
//
//   make ui-align    (bash scripts/fixture-hub.sh node scripts/measure-alignment.mjs)
//
// It reports, page by page:
//
// - glyph: the letter or icon in a small round or chip control, and how far
//   its inked box sits from the control's centre. The ink is measured on
//   pixels, not font metrics: each control is shot at 4× with its glyph and
//   again with the glyph made transparent, and the pixels that differ are
//   the ink. A font's ascent and descent say where its line box is, not
//   where a letter is drawn.
// - row: controls side by side in one row (button, select, input) whose
//   heights or tops differ.
//
// It is a report, not a check: it exits 0 whatever it finds, as some
// offsets are put in on purpose. A PR that changes rendered layout attaches
// the report and says which offsets are meant (CONVENTIONS.md, Web).
import { chromium } from 'playwright'
import { readFileSync } from 'node:fs'

const base = process.env.SPOOL_URL ?? 'http://127.0.0.1:5261'
const dataDir = process.env.SPOOL_DATA_DIR
if (!dataDir) {
  console.error('measure-alignment: SPOOL_DATA_DIR is required (the fixture hub’s data directory)')
  process.exit(2)
}
// Read, never printed: the password the fixture gave its users (#674).
const password = readFileSync(`${dataDir}/fixture-user-password`, 'utf8').trim()

// What counts as off: more than half a CSS pixel.
const TOLERANCE = 0.5
// The scale the glyphs are shot at, so a quarter pixel is one device pixel.
const SCALE = 4

const PAGES = [
  '/',
  '/new',
  '/loops/gardener',
  '/channels',
  '/channels/fleet',
  '/connections',
  '/rules',
  '/settings',
  '/activity',
]

// The box, in device pixels, of the pixels that differ between two PNG
// shots of the same element, or null when none do. Run in the page, so the
// browser decodes the shots.
async function inkBox([withGlyph, without]) {
  const pixels = async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
    const bitmap = await createImageBitmap(new Blob([bytes], { type: 'image/png' }))
    const canvas = new OffscreenCanvas(bitmap.width, bitmap.height)
    const g = canvas.getContext('2d')
    g.drawImage(bitmap, 0, 0)
    return g.getImageData(0, 0, bitmap.width, bitmap.height)
  }
  const a = await pixels(withGlyph)
  const b = await pixels(without)
  if (a.width !== b.width || a.height !== b.height) return null
  let left = Infinity
  let top = Infinity
  let right = -Infinity
  let bottom = -Infinity
  for (let y = 0; y < a.height; y++) {
    for (let x = 0; x < a.width; x++) {
      const i = (y * a.width + x) * 4
      // a faint edge of antialiasing is ink too, but not noise of one level
      const d =
        Math.abs(a.data[i] - b.data[i]) +
        Math.abs(a.data[i + 1] - b.data[i + 1]) +
        Math.abs(a.data[i + 2] - b.data[i + 2])
      if (d > 12) {
        left = Math.min(left, x)
        right = Math.max(right, x + 1)
        top = Math.min(top, y)
        bottom = Math.max(bottom, y + 1)
      }
    }
  }
  return left === Infinity ? null : { left, top, right, bottom, width: a.width, height: a.height }
}

// Marks the controls to measure on the page in view, and returns them.
// glyph: a small control whose content is one or two characters or one
// icon. row: two or more controls side by side as children of one row.
function findControls() {
  const visible = (el) => {
    const r = el.getBoundingClientRect()
    const s = getComputedStyle(el)
    return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none'
  }
  // The text a reader sees in el: none from a part hidden, collapsed or
  // faded out, as a peek label is until hover.
  const shownText = (el) => {
    let out = ''
    const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
    for (let t = walk.nextNode(); t; t = walk.nextNode()) {
      let shown = true
      for (let p = t.parentElement; p && shown; p = p === el ? null : p.parentElement) {
        const ps = getComputedStyle(p)
        if (ps.display === 'none' || ps.visibility === 'hidden' || parseFloat(ps.opacity) === 0) shown = false
      }
      if (shown) out += t.textContent
    }
    return out.trim()
  }
  const describe = (el) => {
    const label =
      el.getAttribute('aria-label') ||
      el.getAttribute('title') ||
      el.textContent.trim().slice(0, 24) ||
      el.getAttribute('placeholder') ||
      ''
    const cls = [...el.classList].slice(0, 2).join('.')
    return `${el.tagName.toLowerCase()}${cls ? '.' + cls : ''}${label ? ` "${label}"` : ''}`
  }
  const glyphs = []
  let n = 0
  for (const el of document.querySelectorAll('body *')) {
    if (!visible(el) || el.closest('svg')) continue
    const r = el.getBoundingClientRect()
    if (r.width > 48 || r.height > 48 || r.width < 10 || r.height < 10) continue
    const s = getComputedStyle(el)
    const round = parseFloat(s.borderTopLeftRadius) >= Math.min(r.width, r.height) * 0.3
    const control = el.matches('button, a, [role=button], summary, label')
    if (!round && !control) continue
    // the shape must be drawn, or there is no centre for the eye to read:
    // a fill, a border, or rings drawn in an SVG beside the glyph
    const svgs = [...el.querySelectorAll('svg')].filter(visible)
    const text = shownText(el)
    const drawn =
      s.backgroundColor !== 'rgba(0, 0, 0, 0)' ||
      parseFloat(s.borderTopWidth) > 0 ||
      s.backgroundImage !== 'none' ||
      (text.length > 0 && svgs.some((v) => v.querySelector('circle')))
    if (!drawn) continue
    // the glyph is a letter or two, or a lone icon
    let hide
    if (text.length >= 1 && text.length <= 2) hide = 'text'
    else if (text.length === 0 && svgs.length === 1) hide = 'svg'
    else continue
    // a smaller glyph control inside this one is measured on its own
    if (glyphs.some((g) => g.el.contains(el) || el.contains(g.el))) continue
    el.dataset.alignId = String(n++)
    glyphs.push({ el, id: el.dataset.alignId, name: describe(el), hide })
  }

  const isRowControl = (el) =>
    el.matches(
      'button, select:not([multiple]):not([size]), input:not([type=checkbox]):not([type=radio]):not([type=range]):not([type=file]):not([type=hidden]), a.btn',
    ) && visible(el)
  const rows = []
  for (const parent of document.querySelectorAll('body *')) {
    const s = getComputedStyle(parent)
    if (!s.display.includes('flex') || s.flexDirection.startsWith('column')) continue
    const kids = [...parent.children].filter(isRowControl)
    if (kids.length < 2) continue
    const boxes = kids.map((k) => ({ name: describe(k), r: k.getBoundingClientRect() }))
    // side by side: each overlaps the first vertically
    const first = boxes[0].r
    const inRow = boxes.filter((b) => b.r.top < first.bottom && b.r.bottom > first.top)
    if (inRow.length < 2) continue
    rows.push({
      parent: describe(parent),
      controls: inRow.map((b) => ({ name: b.name, top: b.r.top, height: b.r.height })),
    })
  }
  return {
    glyphs: glyphs.map((g) => ({ id: g.id, name: g.name, hide: g.hide })),
    rows,
  }
}

const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: 1180, height: 900 }, deviceScaleFactor: SCALE })
const page = await ctx.newPage()
await page.goto(base + '/', { waitUntil: 'networkidle' })
await page.fill('#username', 'admin')
await page.fill('#password', password)
await page.click('button[type=submit]')
await page.waitForSelector('.topbar', { timeout: 15000 })
// Nothing moves while it is measured: no transitions, no caret.
const STILL = '*{transition:none!important;animation:none!important;caret-color:transparent!important}'

const fmt = (v) => (v >= 0 ? '+' : '') + v.toFixed(2)
const lines = []
let count = 0
for (const path of PAGES) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  await page.addStyleTag({ content: STILL })
  await page.waitForTimeout(400)
  const { glyphs, rows } = await page.evaluate(findControls)
  const found = []

  for (const g of glyphs) {
    const el = page.locator(`[data-align-id="${g.id}"]`)
    // the whole element, hovered nowhere: the pointer rests off the page
    await page.mouse.move(0, 0)
    const shot = await el.screenshot({ animations: 'disabled' }).catch(() => null)
    if (!shot) continue
    // the glyph made transparent, and nothing else: a letter's fill goes,
    // not its color, so a ring drawn in currentColor stays in both shots
    await el.evaluate((node, hide) => {
      node.dataset.alignHide = '1'
      const style = document.createElement('style')
      style.id = 'align-hide'
      style.textContent =
        hide === 'text'
          ? '[data-align-hide],[data-align-hide] *{-webkit-text-fill-color:transparent!important;text-shadow:none!important}'
          : '[data-align-hide] svg{visibility:hidden!important}'
      document.head.append(style)
    }, g.hide)
    const bare = await el.screenshot({ animations: 'disabled' }).catch(() => null)
    await el.evaluate((node) => {
      delete node.dataset.alignHide
      document.getElementById('align-hide')?.remove()
    })
    if (!bare) continue
    const ink = await page.evaluate(inkBox, [shot.toString('base64'), bare.toString('base64')])
    if (!ink) continue
    const dx = ((ink.left + ink.right) / 2 - ink.width / 2) / SCALE
    const dy = ((ink.top + ink.bottom) / 2 - ink.height / 2) / SCALE
    if (Math.abs(dx) > TOLERANCE || Math.abs(dy) > TOLERANCE) {
      found.push(`  glyph  ${g.name}: ink centre off by x ${fmt(dx)}px, y ${fmt(dy)}px`)
    }
  }

  for (const row of rows) {
    const heights = row.controls.map((c) => c.height)
    const tops = row.controls.map((c) => c.top)
    const dh = Math.max(...heights) - Math.min(...heights)
    const dt = Math.max(...tops) - Math.min(...tops)
    if (dh > TOLERANCE || dt > TOLERANCE) {
      const each = row.controls.map((c) => `${c.name} h ${c.height.toFixed(2)} top ${c.top.toFixed(2)}`)
      found.push(
        `  row    in ${row.parent}: heights differ by ${dh.toFixed(2)}px, tops by ${dt.toFixed(2)}px`,
      )
      for (const e of each) found.push(`           ${e}`)
    }
  }

  lines.push(`${path}  (${glyphs.length} glyph controls, ${rows.length} rows measured)`)
  lines.push(...(found.length ? found : ['  nothing over 0.5px']))
  count += found.filter((l) => !l.startsWith('           ')).length
}
await browser.close()

console.log(`measure-alignment: offsets over ${TOLERANCE}px on the fixture hub, ${count} found\n`)
console.log(lines.join('\n'))
