import { useUserStore } from '@/stores/useUserStore'
import { TRAFFIC_AGENT, TRAFFIC_SIDECAR } from '@/utils/ruleTraffic'
import gateway from './gateway'
import controlPlane from './controlPlane'
import { useSidecarsEnabled } from './sidecars'

/**
 * Application modes.
 *
 * One bundle renders as one of two products. The backend decides which through
 * `application_mode` (/publicserverinfo before login, /serverinfo after), the
 * store keeps it in `useUserStore.appMode`, and this module is the only reader.
 *
 * A mode is `{ id, theme, postLoginPath, postSetupPath, Page, Guard, Home,
 * Onboarding, CatchAll }` (./gateway.jsx, ./controlPlane.jsx): the shell a page
 * renders in and the three leaves that differ. Every other difference is a
 * pair of sibling files (Gateway…, ControlPlane…) picked in Router.jsx through
 * <ByProduct>.
 * Pages never read `appMode`.
 */

export const DEFAULT_APP_MODE = 'gateway'

const MODES = { gateway, 'control-plane': controlPlane }

// Non-hook getter for callbacks and effects (auth pages). Unknown values fall
// back to the gateway, like a missing field does.
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

const AGENT_ONLY = [TRAFFIC_AGENT]
const SIDECAR_ONLY = [TRAFFIC_SIDECAR]
const AGENT_AND_SIDECAR = [TRAFFIC_AGENT, TRAFFIC_SIDECAR]

// Which traffic a guardrail, masking or analyzer rule can protect here. A control
// plane has no agents.
export function useRuleTraffics() {
  const { id } = useModeConfig()
  const sidecars = useSidecarsEnabled()
  if (id === 'control-plane') return SIDECAR_ONLY
  return sidecars ? AGENT_AND_SIDECAR : AGENT_ONLY
}
