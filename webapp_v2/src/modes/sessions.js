import { useUserStore } from '@/stores/useUserStore'

// The Sessions pages, the session details and the bridge that opens them from
// ClojureScript follow this flag. Off, the ClojureScript app keeps every route.
export const SESSIONS_REACT_FLAG = 'experimental.sessions_react'

export const sessionsReactEnabled = (state = useUserStore.getState()) =>
  Boolean(state.featureFlags?.[SESSIONS_REACT_FLAG])

export const useSessionsReactEnabled = () => useUserStore(sessionsReactEnabled)
