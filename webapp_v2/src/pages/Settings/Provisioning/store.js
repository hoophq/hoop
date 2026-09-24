import { create } from 'zustand'
import { provisioningService } from '@/services/provisioning'
import { useAuthStore } from '@/stores/useAuthStore'

const EMPTY = {
  scim: null,
  sync: null,
  // 'idle' | 'loading' | 'success' | 'error'
  status: 'idle',
  // Shown once, right after it is generated.
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

  load: async () => {
    set({ status: 'loading' })
    try {
      const [scim, sync] = await Promise.all([
        provisioningService.getScim(),
        provisioningService.getDirectorySync(),
      ])
      set({ scim: scim.data, sync: sync.data, status: 'success' })
    } catch {
      set({ status: 'error' })
    }
  },

  generateToken: async () => {
    set({ saving: true })
    try {
      const { data } = await provisioningService.createScimToken()
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
      set({ saving: false, sync: data, groups: [], groupsStatus: 'idle' })
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
}))

// The token and the provider settings belong to the organization that loaded
// them; the next user in the same tab must not see them.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useProvisioningStore.getState().reset()
  }
})
