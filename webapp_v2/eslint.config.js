import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import { defineConfig, globalIgnores } from 'eslint/config'

// ── Product boundary (see CLAUDE.md, "Application modes") ─────────────────────
// A file without a product prefix serves both products. When one product needs
// it changed, it becomes two siblings: Gateway* and ControlPlane*. These rules
// turn the name into a contract. Matching is case-sensitive on purpose: the
// data files gatewayNav.js/controlPlaneNav.js are listed where they matter.

// Modules only the gateway may reach: its sibling files, the ClojureScript app
// and its bridge, and the chrome that only makes sense with a data plane.
const GATEWAY_ONLY = [
  '**/Gateway*',
  '**/gatewayNav',
  '@/components/ClojureApp',
  '@/utils/clojureDispatch',
  '@/stores/useBridgeStore',
  '@/features/NativeConnections',
  '@/features/NativeConnections/**',
  '@/layout/Sidebar/ConfigStatus',
  '@/layout/Sidebar/ConfigStatus/**',
  '@/features/CommandPalette/spotlight',
]

const CONTROL_PLANE_ONLY = ['**/ControlPlane*', '**/controlPlaneNav']

const PRODUCT_FILES = {
  gateway: ['src/**/Gateway*.{js,jsx}', 'src/**/gatewayNav.js'],
  controlPlane: ['src/**/ControlPlane*.{js,jsx}', 'src/**/controlPlaneNav.js'],
}

const restricted = (patterns, message) => ({
  'no-restricted-imports': ['error', { patterns: [{ group: patterns, message, caseSensitive: true }] }],
})

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
      'no-unused-vars': ['error', { varsIgnorePattern: '^[A-Z_]' }],
    },
  },
  // Which product the bundle renders as is read in src/modes only. Pages, layout
  // and features never branch on it; a difference is a sibling file instead.
  {
    files: ['src/**/*.{js,jsx}'],
    ignores: ['src/modes/**', 'src/stores/useUserStore.js'],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: "MemberExpression[property.name='appMode'], ObjectPattern > Property[key.name='appMode']",
          message: 'appMode is read in src/modes only. Pick the product with a sibling file (Gateway*/ControlPlane*) in Router.jsx, not with a check on the mode.',
        },
      ],
    },
  },
  {
    files: PRODUCT_FILES.controlPlane,
    rules: restricted(GATEWAY_ONLY, 'Gateway-only module. A ControlPlane* file uses its own sibling, never ClojureScript.'),
  },
  {
    files: PRODUCT_FILES.gateway,
    rules: restricted(CONTROL_PLANE_ONLY, 'Control-plane-only module. A Gateway* file uses its own sibling.'),
  },
  // A shared file serves both products, so it cannot lean on either side. The
  // two exceptions are where the product is chosen: the manifests and Router.jsx.
  {
    files: ['src/**/*.{js,jsx}'],
    ignores: [...PRODUCT_FILES.gateway, ...PRODUCT_FILES.controlPlane, 'src/modes/**', 'src/Router.jsx'],
    rules: restricted(
      ['**/Gateway*', '**/ControlPlane*'],
      'This file has no product prefix, so it serves both products and cannot import one side. If it needs to differ, make it a Gateway*/ControlPlane* pair chosen in Router.jsx.',
    ),
  },
])
