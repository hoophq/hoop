import { create } from 'zustand'
import { provisioningService } from '@/services/provisioning'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY = {
  scim: null,
  sync: null,
  // Whether the Slack import or SCIM manages the org's groups.
  groupsManaged: false,
  // 'idle' | 'loading' | 'success' | 'error'
  status: 'idle',
  // Shown once, right after it is generated; cleared on every load and when
  // the SCIM section unmounts, so it never outlives the view that showed it.
  newToken: null,
  groups: [],
  groupsStatus: 'idle',
  groupsError: null,
  saving: false,
  running: false,
}

function message(error) {
  return error?.response?.data?.message || error?.message
}

export const useProvisioningStore = create((set, get) => ({
  ...EMPTY,

  reset: () => set({ ...EMPTY }),

  clearNewToken: () => set({ newToken: null }),

  load: async () => {
    set({ status: 'loading', newToken: null })
    try {
      const [scim, sync, status] = await Promise.all([
        provisioningService.getScim(),
        provisioningService.getDirectorySync(),
        provisioningService.getStatus(),
      ])
      set({
        scim: scim.data,
        sync: sync.data,
        groupsManaged: !!status.data?.groups_managed,
        status: 'success',
      })
    } catch {
      set({ status: 'error' })
    }
  },

  refreshStatus: async () => {
    try {
      const { data } = await provisioningService.getStatus()
      set({ groupsManaged: !!data?.groups_managed })
    } catch {
      // The page keeps the last known state; the next load retries.
    }
  },

  generateToken: async () => {
    set({ saving: true })
    try {
      const { data } = await provisioningService.putScimToken()
      const { data: scim } = await provisioningService.getScim()
      set({ saving: false, newToken: data.token, scim })
      return { ok: true }
    } catch (error) {
      set({ saving: false })
      return { ok: false, error: message(error) }
    }
  },

  deleteToken: async () => {
    set({ saving: true })
    try {
      await provisioningService.deleteScim()
      const { data: scim } = await provisioningService.getScim()
      set({ saving: false, newToken: null, scim })
      return { ok: true }
    } catch (error) {
      set({ saving: false })
      return { ok: false, error: message(error) }
    }
  },

  saveSync: async (payload) => {
    set({ saving: true })
    try {
      const { data } = await provisioningService.saveDirectorySync(payload)
      set({ saving: false, sync: data })
      return { ok: true }
    } catch (error) {
      set({ saving: false })
      return { ok: false, error: message(error) }
    }
  },

  deleteSync: async () => {
    set({ saving: true })
    try {
      await provisioningService.deleteDirectorySync()
      const { data } = await provisioningService.getDirectorySync()
      set({ saving: false, sync: data })
      return { ok: true }
    } catch (error) {
      set({ saving: false })
      return { ok: false, error: message(error) }
    }
  },

  runSync: async () => {
    set({ running: true })
    try {
      const { data } = await provisioningService.runDirectorySync()
      set({ running: false, sync: data })
      get().refreshStatus()
      return { ok: !data?.last_error, error: data?.last_error }
    } catch (error) {
      set({ running: false })
      return { ok: false, error: message(error) }
    }
  },

  loadGroups: async () => {
    if (get().groupsStatus === 'loading') return
    set({ groupsStatus: 'loading', groupsError: null })
    try {
      const { data } = await provisioningService.listDirectoryGroups()
      set({ groups: Array.isArray(data) ? data : [], groupsStatus: 'success' })
    } catch (error) {
      set({ groupsStatus: 'error', groupsError: message(error) })
    }
  },

  stopManagingGroups: async () => {
    set({ saving: true })
    try {
      await provisioningService.stopManagingGroups()
      set({ saving: false, groupsManaged: false })
      return { ok: true }
    } catch (error) {
      set({ saving: false })
      return { ok: false, error: message(error) }
    }
  },
}))

// The token and the sync belong to the organization that loaded them; the
// next user in the same tab must not see them.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useProvisioningStore.getState().reset()
  }
})
