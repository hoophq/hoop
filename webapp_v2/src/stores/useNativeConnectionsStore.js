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
  error: null,

  open: () => set({ opened: true }),
  close: () => set({ opened: false }),
  toggle: () => set((s) => ({ opened: !s.opened })),

  /** Wipes every trace of the signed-in user. Called on logout. */
  reset: () =>
    set({
      opened: false,
      expanded: null,
      query: '',
      roles: [],
      loading: false,
      error: null,
    }),

  setQuery: (query) => set({ query }),
  clearQuery: () => set({ query: '' }),
  setExpanded: (expanded) => set({ expanded }),

  /** Opens the drawer with one row already expanded and its flow started. */
  openConnection: (connectionName) => set({ opened: true, expanded: connectionName }),

  /**
   * Loads the full connection list and keeps the natively-connectable subset.
   *
   * Revalidates on every call — the drawer calls it each time it opens. There
   * is no cache to invalidate because there is nothing to invalidate it FROM:
   * resources are still created on the ClojureScript side, and a role can also
   * appear because an admin granted it in another tab or another session. A
   * fetched-once list answered all of those with a stale answer until the user
   * reloaded the page. The previous rows stay on screen while the request is in
   * flight (RoleList only shows skeletons when there is nothing to show), so
   * revalidating costs a request, not a flicker.
   *
   * Deliberately the non-paginated endpoint: the paginated one applies an
   * access-control-group join that can hide a connection the user can actually
   * see (see services/connections.js), and neither endpoint can filter on
   * access_mode_connect or on a set of subtypes, so the client has to see
   * everything to apply the policy and report an honest total.
   */
  load: async () => {
    if (get().loading) return
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
      set({ roles })
    } catch (error) {
      set({ error: error?.message || 'Failed to load resource roles' })
    } finally {
      set({ loading: false })
    }
  },
}))
