// Checks the no-em-dash rule in eslint.config.js against the phrasings it
// must catch and the ones it must leave alone (CONVENTIONS.md, #331, #1013).
// A selector that stopped matching would pass every file green, so each
// phrasing here is linted as a file under src/ with the repo's own config.
//
// Run from web/: `node scripts/em-dash-test.mjs`. Exits 1 on a wrong case.
import { ESLint } from 'eslint'

const caught = {
  'a string literal': `export const label = 'Saved — reload to see it'`,
  'a string with an escape': `export const label = 'Saved \\u2014 reload'`,
  'a template literal': 'export const line = (n: number) => `${n} loops — idle`',
  'JSX text': `export const Row = () => <p>Saved — reload to see it</p>`,
  'JSX text with an entity': `export const Row = () => <p>Saved &mdash; reload</p>`,
  'a title attribute': `export const Row = () => <span title="sent — not read" />`,
  'a placeholder attribute': `export const Field = () => <input placeholder="name — optional" />`,
  'an attribute expression': `export const Row = () => <span title={'sent — not read'} />`,
}

const passed = {
  'a line comment': `// Saved — reload\nexport const label = 'Saved'`,
  'a block comment': `/* Saved — reload */\nexport const label = 'Saved'`,
  'a JSX comment': `export const Row = () => (\n  <p>\n    {/* Saved — reload */}\n    Saved\n  </p>\n)`,
  'an en dash': `export const label = '1–3 loops'`,
  'a middle dot': `export const label = 'opus · deep agentic work'`,
}

const eslint = new ESLint()
let failed = 0

async function hits(code) {
  const [result] = await eslint.lintText(code, { filePath: 'src/emDashCase.tsx' })
  const fatal = result.messages.filter((message) => message.fatal)
  if (fatal.length > 0) throw new Error(`does not parse: ${fatal[0].message}\n${code}`)
  return result.messages.filter((message) => message.ruleId === 'no-restricted-syntax').length
}

for (const [name, code] of Object.entries(caught)) {
  const count = await hits(code)
  if (count !== 1) {
    console.log(`FAIL ${name}: ${count} findings, want 1`)
    failed++
  } else console.log(`ok   ${name}`)
}
for (const [name, code] of Object.entries(passed)) {
  const count = await hits(code)
  if (count !== 0) {
    console.log(`FAIL ${name}: ${count} findings, want none`)
    failed++
  } else console.log(`ok   ${name}`)
}

// A test file may name the character to assert it is absent.
const [testResult] = await eslint.lintText(`export const banned = '\\u2014'`, {
  filePath: 'src/emDashCase.test.ts',
})
if (testResult.messages.some((message) => message.ruleId === 'no-restricted-syntax')) {
  console.log('FAIL a test file: flagged')
  failed++
} else console.log('ok   a test file')

if (failed > 0) process.exit(1)
console.log('all cases pass')
