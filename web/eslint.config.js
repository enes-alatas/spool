import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import reactHooks from 'eslint-plugin-react-hooks'

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
    // names the three browser globals they use.
    files: ['demo/record.mjs'],
    languageOptions: {
      globals: {
        console: 'readonly',
        process: 'readonly',
        URL: 'readonly',
        document: 'readonly',
        addEventListener: 'readonly',
        requestAnimationFrame: 'readonly',
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
)
