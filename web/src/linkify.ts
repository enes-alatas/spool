// A message's text cut into runs of plain text and web links (#759). Text in
// the room is untrusted — a loop's words, or a stranger's — so nothing in it
// is ever read as markup: the only thing recognised is an http(s) URL, and it
// is shown as itself, so a link can never say one place and go to another.

export type TextRun = { kind: 'text'; text: string } | { kind: 'link'; href: string }

// A URL runs to the next space. Angle brackets and quotes end it too: they
// are how prose wraps a URL, and none of them is legal unescaped in one.
const URL_RE = /https?:\/\/[^\s<>"'`]+/gi

// Punctuation that closes the sentence a URL sits in rather than the URL, and
// the asterisks of markdown emphasis, which loops write: "**https://x/y**".
const TRAILING = /[.,;:!?*]$/

// Brackets a URL may close only if it opened them.
const CLOSERS: Record<string, string> = { ')': '(', ']': '[' }

// What of a matched URL is the URL: trailing sentence punctuation is dropped,
// and so is a closing bracket the URL never opened, which is the one wrapping
// it — "(see https://x/y)", "[https://x/y]". A balanced pair stays, so a
// Wikipedia page named "Foo_(bar)" keeps its last character.
function trimUrl(url: string): string {
  for (;;) {
    const last = url.slice(-1)
    const opener = CLOSERS[last]
    if (TRAILING.test(url) || (opener && count(url, last) > count(url, opener))) {
      url = url.slice(0, -1)
    } else {
      return url
    }
  }
}

function count(s: string, ch: string): number {
  return s.split(ch).length - 1
}

export function linkRuns(text: string): TextRun[] {
  const runs: TextRun[] = []
  let last = 0
  for (const m of text.matchAll(URL_RE)) {
    const href = trimUrl(m[0])
    // A scheme with nothing after it ("see https://.") names no place.
    if (href.length <= href.indexOf('//') + 2) continue
    if (m.index > last) runs.push({ kind: 'text', text: text.slice(last, m.index) })
    runs.push({ kind: 'link', href })
    last = m.index + href.length
  }
  if (last < text.length) runs.push({ kind: 'text', text: text.slice(last) })
  return runs
}
