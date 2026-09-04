import { useUserStore } from '@/stores/useUserStore'
import gateway from './gateway'
import controlPlane from './controlPlane'

/**
 * Application modes.
 *
 * One bundle renders as one of two products. The backend decides which through
 * `application_mode` (/publicserverinfo before login, /serverinfo after), the
 * store keeps it in `useUserStore.appMode`, and this module is the only reader.
 *
 * A mode is a manifest — see ./gateway.js for the shape — and everything that
 * varies between products (sidebar sections, palette items, landing route,
 * catch-all, gateway-only chrome, theme, post-login paths) is a key in it.
 * Components ask `useModeConfig()` for the manifest; callbacks and effects use
 * `getModeConfig()`. Pages never read `appMode`.
 */

export const DEFAULT_APP_MODE = 'gateway'

const MODES = { gateway, 'control-plane': controlPlane }

// Non-hook getter for callbacks and effects (auth pages, ProtectedRoute).
// Unknown values fall back to the gateway, like a missing field does.
export function getModeConfig(mode = useUserStore.getState().appMode) {
  return MODES[mode] ?? MODES[DEFAULT_APP_MODE]
}

export function useModeConfig() {
  const appMode = useUserStore((s) => s.appMode)
  return getModeConfig(appMode)
}
