import { create } from 'zustand'
import infrastructure from '@/services/infrastructure'

// Page-local store for Settings → Infrastructure. Owns every service
// interaction (components talk to the store, never to services) and the
// last server snapshot of analytics/hide-role-info, which save() uses to
// send the org-level PUTs only when the value actually changed. Actions
// rethrow on failure so the page decides how to surface errors.
export const useInfrastructureStore = create((set, get) => ({
  loading: true,
  saving: false,
  analyticsMode: '',
  hideRoleInfo: false,

  // Loads the three server resources and returns a snapshot the page can
  // seed its form drafts from.
  load: async () => {
    set({ loading: true })
    try {
      const [misc, analytics, hideRole] = await Promise.all([
        infrastructure.get(),
        infrastructure.getAnalyticsMode(),
        infrastructure.getHideRoleInfo(),
      ])
      const analyticsMode = analytics.data?.analytics_mode ?? ''
      const hideRoleInfo = hideRole.data?.hide_role_info ?? false
      set({ analyticsMode, hideRoleInfo })
      return { misc: misc.data ?? {}, analyticsMode, hideRoleInfo }
    } finally {
      set({ loading: false })
    }
  },

  save: async ({ miscPayload, analyticsMode, hideRoleInfo }) => {
    set({ saving: true })
    try {
      const requests = [infrastructure.update(miscPayload)]
      if (analyticsMode && analyticsMode !== get().analyticsMode) {
        requests.push(infrastructure.updateAnalyticsMode(analyticsMode))
      }
      if (hideRoleInfo !== get().hideRoleInfo) {
        requests.push(infrastructure.updateHideRoleInfo(hideRoleInfo))
      }
      await Promise.all(requests)
      set({ analyticsMode, hideRoleInfo })
    } finally {
      set({ saving: false })
    }
  },
}))
