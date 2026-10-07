import { useUserStore } from '@/stores/useUserStore'

const AGENTS_FLAG = 'experimental.agents'

// Every gateway serves sidecars. The agents product (agent onboarding) is the
// one the flag turns on; it is set once for every org that already had an agent.
export const agentsEnabled = (state = useUserStore.getState()) =>
  Boolean(state.featureFlags?.[AGENTS_FLAG])
