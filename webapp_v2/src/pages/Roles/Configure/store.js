import { create } from 'zustand'
import { connectionsService } from '@/services/connections'

// Local store for the Configure Role page.
//
// Lives next to the page (not in /stores/) because nothing outside this
// route consumes it. Mirrors the shape of the CLJS :connection-setup
// re-frame slice but only models the pieces this React page touches.
//
// Staged-secrets contract:
//   stagedSecrets[key] = { action: 'replace' | 'delete' | 'new', value?: string }
//   - 'replace' overrides an existing inline secret (value is base64-encoded).
//   - 'delete'  removes an existing key (custom connection type only).
//   - 'new'     adds a brand-new custom-type secret (key + value).
// The staged map is consumed by save() and translated into a PATCH payload.

const initialState = {
  connection: null,
  loading: false,
  error: null,
  saving: false,
  deleting: false,
  testing: false,
  testResult: null,
  testModalOpen: false,
  stagedSecrets: {},
}

export const useConfigureRoleStore = create((set, get) => ({
  ...initialState,

  loadConnection: async (nameOrId) => {
    set({ loading: true, error: null, connection: null, stagedSecrets: {} })
    try {
      const data = await connectionsService.getConnection(nameOrId)
      set({ connection: data, loading: false })
    } catch (err) {
      const message = err?.response?.data?.message || 'Failed to load connection.'
      set({ error: message, loading: false })
    }
  },

  replaceSecret: (key, base64Value) => {
    set((state) => {
      const isExisting = state.connection?.secret && key in state.connection.secret
      return {
        stagedSecrets: {
          ...state.stagedSecrets,
          [key]: {
            action: isExisting ? 'replace' : 'new',
            value: base64Value,
          },
        },
      }
    })
  },

  deleteSecret: (key) => {
    set((state) => ({
      stagedSecrets: { ...state.stagedSecrets, [key]: { action: 'delete' } },
    }))
  },

  cancelSecretChange: (key) => {
    set((state) => {
      const next = { ...state.stagedSecrets }
      delete next[key]
      return { stagedSecrets: next }
    })
  },

  hasPendingChanges: () => Object.keys(get().stagedSecrets).length > 0,

  // Builds the secrets sub-payload for the PATCH request.
  // Empty value = delete (per backend mergeSecrets semantics).
  buildSecretsPatch: () => {
    const staged = get().stagedSecrets
    const out = {}
    for (const [key, change] of Object.entries(staged)) {
      if (change.action === 'delete') {
        out[key] = ''
      } else {
        out[key] = change.value
      }
    }
    return out
  },

  save: async () => {
    const { connection, stagedSecrets } = get()
    if (!connection) return
    set({ saving: true, error: null })
    try {
      const payload = {}
      if (Object.keys(stagedSecrets).length > 0) {
        payload.secret = get().buildSecretsPatch()
      }
      const updated = await connectionsService.patchConnection(connection.name, payload)
      set({ connection: updated, stagedSecrets: {}, saving: false })
      return updated
    } catch (err) {
      const message = err?.response?.data?.message || 'Failed to save connection.'
      set({ error: message, saving: false })
      throw err
    }
  },

  deleteConnection: async () => {
    const { connection } = get()
    if (!connection) return
    set({ deleting: true, error: null })
    try {
      await connectionsService.deleteConnection(connection.name)
      set({ deleting: false })
    } catch (err) {
      const message = err?.response?.data?.message || 'Failed to delete connection.'
      set({ error: message, deleting: false })
      throw err
    }
  },

  runTestConnection: async () => {
    const { connection } = get()
    if (!connection) return
    const startedAt = Date.now()
    set({ testing: true, testResult: null, testModalOpen: true })
    try {
      const result = await connectionsService.testConnection(connection.name)
      set({
        testing: false,
        testResult: {
          success: true,
          durationMs: Date.now() - startedAt,
          ...result,
        },
      })
    } catch (err) {
      const message = err?.response?.data?.message || 'Connection test failed.'
      set({
        testing: false,
        testResult: { success: false, message, durationMs: Date.now() - startedAt },
      })
    }
  },

  closeTestModal: () => set({ testModalOpen: false, testResult: null }),

  clearTestResult: () => set({ testResult: null }),

  reset: () => set(initialState),
}))
