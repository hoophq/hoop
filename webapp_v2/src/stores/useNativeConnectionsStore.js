import { create } from 'zustand'
import { connectionsService } from '@/services/connections'
import { isNativeCapableSubtype } from '@/utils/connectionPolicy'
import { useUserStore } from '@/stores/useUserStore'
import { buildSearchIndex } from '@/features/NativeConnections/helpers'

/**
 * Owns "which roles exist and are natively connectable", plus the drawer's own
 * open/search/expanded state.
 *
 * Credential state lives in useNativeAccessStore — this store never touches
 * secrets or the credential lifecycle.
 */
export const useNativeConnectionsStore = create((set, get) => ({
  opened: false,
  // Accordion value; also the connection name of the expanded row.
  expanded: null,
  query: '',

  roles: [],
  loading: false,
  loaded: false,
  error: null,

  open: () => set({ opened: true }),
  close: () => set({ opened: false }),
  toggle: () => set((s) => ({ opened: !s.opened })),

  setQuery: (query) => set({ query }),
  clearQuery: () => set({ query: '' }),
  setExpanded: (expanded) => set({ expanded }),

  /** Opens the drawer with one row already expanded and its flow started. */
  openConnection: (connectionName) => set({ opened: true, expanded: connectionName }),

  /**
   * Loads the full connection list and keeps the natively-connectable subset.
   *
   * Deliberately the non-paginated endpoint: the paginated one applies an
   * access-control-group join that can hide a connection the user can actually
   * see (see services/connections.js), and neither endpoint can filter on
   * access_mode_connect or on a set of subtypes, so the client has to see
   * everything to apply the policy and report an honest total.
   */
  load: async ({ force = false } = {}) => {
    if (get().loading) return
    if (get().loaded && !force) return
    set({ loading: true, error: null })
    try {
      const connections = await connectionsService.getConnections()
      const { postgresProxyEnabled } = useUserStore.getState()
      // Filtered on protocol capability only. access_mode_connect is recorded
      // rather than filtered on, so a role whose native access was switched off
      // while a credential is still live can keep its row (and its Disconnect).
      const roles = connections
        .filter((c) => isNativeCapableSubtype(c, { postgresProxyEnabled }))
        .map((c) => ({
          id: c.id,
          name: c.name,
          subtype: c.subtype,
          type: c.type,
          status: c.status,
          resourceName: c.resource_name,
          accessModeConnect: c.access_mode_connect,
          reviewers: c.reviewers ?? [],
          // Precomputed once so filtering is a single String.includes per row.
          searchIndex: buildSearchIndex(c),
        }))
        .sort((a, b) => a.name.localeCompare(b.name))
      set({ roles, loaded: true })
    } catch (error) {
      set({ error: error?.message || 'Failed to load resource roles' })
    } finally {
      set({ loading: false })
    }
  },

  refresh: () => get().load({ force: true }),
}))
