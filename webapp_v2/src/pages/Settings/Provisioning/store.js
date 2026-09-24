import { create } from 'zustand'
import { provisioningService } from '@/services/provisioning'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY = {
  sync: null,
  // Whether the Slack import manages the org's groups.
  groupsManaged: false,
  // 'idle' | 'loading' | 'success' | 'error'
  status: 'idle',
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

  load: async () => {
    set({ status: 'loading' })
    try {
      const [sync, status] = await Promise.all([
        provisioningService.getDirectorySync(),
        provisioningService.getStatus(),
      ])
      set({
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

// The sync belongs to the organization that loaded it; the next user in the
// same tab must not see it.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useProvisioningStore.getState().reset()
  }
})
