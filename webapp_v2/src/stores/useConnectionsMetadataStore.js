import { create } from 'zustand'
import { connectionsMetadataService } from '@/services/connectionsMetadata'
import { jsonCredentialToField } from '@/utils/connectionsMetadataMapper'

// Loaded once at app start (App.jsx) and consumed by:
// - Credential renderers (Configure Role > Credentials tab) for the
//   field schema of catalog connections.
// - getConnectionIcon for the icon path of any connection.
//
// State is idempotent: load() short-circuits if already loaded or
// in-flight. On fetch failure the store keeps `metadata: null` + an
// `error` string; consumers degrade by showing a clear loading or
// error state.
export const useConnectionsMetadataStore = create((set, get) => ({
  metadata: null,
  loading: false,
  error: null,

  load: async () => {
    if (get().metadata || get().loading) return
    set({ loading: true, error: null })
    try {
      const data = await connectionsMetadataService.fetch()
      set({ metadata: data, loading: false })
    } catch (error) {
      set({ error: error.message || String(error), loading: false })
    }
  },

  // Returns the raw .connections[*] entry whose
  // resourceConfiguration.subtype matches, or null.
  getConnection: (subtype) => {
    const { metadata } = get()
    if (!metadata || !subtype) return null
    const entries = metadata.connections || []
    return (
      entries.find(
        (c) => c.resourceConfiguration?.subtype === subtype
      ) || null
    )
  },

  // Returns React-shaped credential fields for the subtype, or null if
  // the subtype is unknown or has no credentials block.
  getCredentialSchema: (subtype) => {
    const entry = get().getConnection(subtype)
    const credentials = entry?.resourceConfiguration?.credentials
    if (!Array.isArray(credentials) || credentials.length === 0) return null
    return credentials.map(jsonCredentialToField)
  },

  // Returns the icon-name slug for the subtype, or null.
  getIconName: (subtype) => {
    const entry = get().getConnection(subtype)
    return entry?.['icon-name'] || null
  },
}))
