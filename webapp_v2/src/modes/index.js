import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { samePagePath } from '@/utils/redirect'
import { TRAFFIC_AGENT, TRAFFIC_SIDECAR } from '@/utils/ruleTraffic'
import gateway from './gateway'

/**
 * The product manifest. The backend names it through `application_mode`
 * (/publicserverinfo before login, /serverinfo after), the store keeps it in
 * `useUserStore.appMode`, and this module is the only reader. The gateway is
 * the only mode; an unknown value falls back to it.
 *
 * A mode is `{ id, theme, postLoginPath, postSetupPath, Page, Guard, Home,
 * Onboarding, CatchAll }` (./gateway.jsx). Pages never read `appMode`.
 */

export const DEFAULT_APP_MODE = 'gateway'

const MODES = { gateway }

// Non-hook getter for callbacks and effects (auth pages).
export function getModeConfig(mode = useUserStore.getState().appMode) {
  return MODES[mode] ?? MODES[DEFAULT_APP_MODE]
}

export function useModeConfig() {
  const appMode = useUserStore((s) => s.appMode)
  return getModeConfig(appMode)
}

// Where an auth page sends the user next. Waits for the boot-time mode fetch,
// which is a no-op once it has landed: a login submitted before /publicserverinfo
// answered must not read the gateway default. A failed fetch keeps that default.
export async function postAuthPath(kind = 'postLoginPath') {
  await useUserStore.getState().loadAppMode()
  return getModeConfig()[kind]
}

// Sends a person who just signed up back to the page that sent them to auth
// (ProtectedRoute saved it), as Auth/Callback does after a login, else to
// postAuthPath(kind). Only a page of this app, in place: a saved URL that
// points elsewhere is dropped. Cleared either way, so it never replays later.
export async function goAfterAuth(navigate, kind = 'postLoginPath', options) {
  const target = samePagePath(useAuthStore.getState().getAndClearRedirectUrl())
  if (target) {
    navigate(target, { ...options, replace: true })
    return
  }
  navigate(await postAuthPath(kind), options)
}

const AGENT_AND_SIDECAR = [TRAFFIC_AGENT, TRAFFIC_SIDECAR]

// Which traffic a guardrail, masking or analyzer rule can protect. Every
// gateway serves sidecars.
export function useRuleTraffics() {
  return AGENT_AND_SIDECAR
}
