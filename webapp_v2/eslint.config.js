import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{js,jsx}'],
    extends: [
      js.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 'latest',
      globals: globals.browser,
      parserOptions: {
        ecmaVersion: 'latest',
        ecmaFeatures: { jsx: true },
        sourceType: 'module',
      },
    },
    rules: {
      // There is no eslint-plugin-react here, so core no-unused-vars never
      // counts a JSX element as a use of its component. Hence the capitalised
      // exemption — and it applies to a component reached through a PROP
      // (AiSessionAnalyzer takes its Configure tab that way) exactly as it does
      // to an imported one.
      'no-unused-vars': [
        'error',
        { varsIgnorePattern: '^[A-Z_]', argsIgnorePattern: '^[A-Z_]' },
      ],
    },
  },
  // The Vite config runs in Node, not the browser.
  {
    files: ['vite.config.js'],
    languageOptions: { globals: globals.node },
  },
])
