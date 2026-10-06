import { useUserStore } from '@/stores/useUserStore'

const SIDECARS_FLAG = 'beta.sidecar_listeners'

// A control plane serves sidecars before its orgs get the flag.
export const sidecarsEnabled = (state = useUserStore.getState()) =>
  state.appMode === 'control-plane' || Boolean(state.featureFlags?.[SIDECARS_FLAG])

export const useSidecarsEnabled = () => useUserStore(sidecarsEnabled)
