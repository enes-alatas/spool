import { continueRender, delayRender } from 'remotion'
import inter400 from '@fontsource/inter/files/inter-latin-400-normal.woff2'
import inter500 from '@fontsource/inter/files/inter-latin-500-normal.woff2'
import inter600 from '@fontsource/inter/files/inter-latin-600-normal.woff2'
import inter700 from '@fontsource/inter/files/inter-latin-700-normal.woff2'
import mono400 from '@fontsource/jetbrains-mono/files/jetbrains-mono-latin-400-normal.woff2'
import mono500 from '@fontsource/jetbrains-mono/files/jetbrains-mono-latin-500-normal.woff2'
import nunito400 from '@fontsource/nunito/files/nunito-latin-400-normal.woff2'

// The same faces record.mjs pins in the recorded control room, so the
// captions and the footage share one typeface, plus the wordmark's Nunito
// (ADR-0027). The render waits for them.
const faces: [string, string, string][] = [
  ['Inter', inter400, '400'],
  ['Inter', inter500, '500'],
  ['Inter', inter600, '600'],
  ['Inter', inter700, '700'],
  ['JetBrains Mono', mono400, '400'],
  ['JetBrains Mono', mono500, '500'],
  ['Nunito', nunito400, '400'],
]

const handle = delayRender('fonts')
Promise.all(
  faces.map(([family, url, weight]) => {
    const face = new FontFace(family, `url(${url}) format('woff2')`, { weight })
    document.fonts.add(face)
    return face.load()
  }),
).then(() => continueRender(handle))
