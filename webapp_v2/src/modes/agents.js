import { useUserStore } from '@/stores/useUserStore'

// Agent-only nav and palette items carry `featureFlag: AGENTS_FLAG`.
export const AGENTS_FLAG = 'experimental.agents'

// Every gateway serves sidecars. The agents product (agent onboarding) is the
// one the flag turns on; it is set once for every org that already had an agent.
export const agentsEnabled = (state = useUserStore.getState()) =>
  Boolean(state.featureFlags?.[AGENTS_FLAG])

export const useAgentsEnabled = () => useUserStore(agentsEnabled)
