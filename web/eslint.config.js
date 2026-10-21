import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import reactHooks from 'eslint-plugin-react-hooks'

// No em dash in UI text (CONVENTIONS.md, #331): a string, a template or JSX
// text carrying one fails, as the character, an escape or an entity. Comments
// are not nodes, so they pass. scripts/em-dash-test.mjs holds the phrasings
// it must catch and the ones it must leave alone (#1013).
const emDash = '/\\u2014|&mdash;|&#8212;|&#x2014;/i'
const emDashMessage =
  'No em dash in UI text: use a colon, comma, full stop or parentheses (CONVENTIONS.md, #331).'
const noEmDash = [
  { selector: `Literal[value=${emDash}]`, message: emDashMessage },
  { selector: `TemplateElement[value.cooked=${emDash}]`, message: emDashMessage },
  { selector: `JSXText[value=${emDash}]`, message: emDashMessage },
]

export default tseslint.config(
  { ignores: ['dist/**', 'node_modules/**', 'demo/node_modules/**', 'demo/out/**', 'demo/public/**'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    // scripts/ are node tools, not browser code: they read argv, print to
    // stdout and exit (scripts/ui-shots.mjs, #248).
    files: ['scripts/**/*.mjs'],
    // Listed rather than pulled from the `globals` package: three names is
    // less than a dependency.
    languageOptions: { globals: { console: 'readonly', process: 'readonly', URL: 'readonly' } },
  },
  {
    // The README demo's recorder (#447) is a node tool too, and the
    // functions it hands to `page.evaluate` run in the page, so it also
    // names the four browser globals they use.
    files: ['demo/record.mjs'],
    languageOptions: {
      globals: {
        console: 'readonly',
        process: 'readonly',
        URL: 'readonly',
        document: 'readonly',
        addEventListener: 'readonly',
        requestAnimationFrame: 'readonly',
        window: 'readonly',
      },
    },
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    plugins: { 'react-hooks': reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
    },
  },
  {
    // A test may name the character to assert it is absent.
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/**/*.test.{ts,tsx}'],
    rules: { 'no-restricted-syntax': ['error', ...noEmDash] },
  },
)
