import { create } from 'zustand'
import { resourcesService } from '@/services/resources'
import { connectionsService } from '@/services/connections'
import { useAuthStore } from '@/stores/useAuthStore'
import { PAGE_SIZE } from '@/pages/Resources/store'

const emptyRoles = {
  items: [],
  total: 0,
  page: 0,
  loading: false,
  loaded: false,
  error: null,
  failedAppend: false,
}

const initial = {
  name: null,
  resource: null,
  loading: false,
  notFound: false,
  error: null,
  saving: false,
  roles: emptyRoles,
}

// Module-level so a response that outlives open() or reset() is still recognised as stale.
let session = 0
let resourceGen = 0
let rolesGen = 0

const messageOf = (err, fallback) => err?.response?.data?.message || fallback

export function hasMoreRoles(roles) {
  return roles.page * PAGE_SIZE < roles.total
}

export const useConfigureResourceStore = create((set, get) => ({
  ...initial,

  open: (name) => {
    session++
    set({ ...initial, name })
    get().loadResource()
    get().loadRoles()
  },

  loadResource: async () => {
    const gen = ++resourceGen
    set({ loading: true, error: null, notFound: false })
    try {
      const { data } = await resourcesService.get(get().name)
      if (gen !== resourceGen) return
      set({ resource: data, loading: false })
    } catch (err) {
      if (gen !== resourceGen) return
      // An unknown name ends on a not-found state, not a loader (Q8).
      if (err?.response?.status === 404) set({ loading: false, notFound: true })
      else set({ loading: false, error: messageOf(err, 'Failed to load the resource.') })
    }
  },

  loadRoles: async ({ append = false } = {}) => {
    const page = append ? get().roles.page + 1 : 1
    const gen = ++rolesGen
    set((state) => ({ roles: { ...state.roles, loading: true, error: null } }))
    try {
      const { data } = await connectionsService.list({ resource_name: get().name, page, page_size: PAGE_SIZE })
      if (gen !== rolesGen) return
      const rows = data?.data ?? []
      set((state) => ({
        roles: {
          ...emptyRoles,
          items: append ? [...state.roles.items, ...rows] : rows,
          total: data?.pages?.total ?? rows.length,
          page,
          loaded: true,
        },
      }))
    } catch (err) {
      if (gen !== rolesGen) return
      set((state) => ({
        roles: {
          ...state.roles,
          loading: false,
          loaded: true,
          error: messageOf(err, 'Failed to load the roles.'),
          failedAppend: append,
        },
      }))
    }
  },

  loadMoreRoles: () => {
    const { roles } = get()
    if (roles.loading || roles.error || !roles.loaded || !hasMoreRoles(roles)) return undefined
    return get().loadRoles({ append: true })
  },

  retryRoles: () => get().loadRoles({ append: get().roles.failedAppend }),

  // The roles follow the rename in the database; the list reloads under the new name (Q7).
  rename: async (newName) => {
    const current = session
    const { resource } = get()
    set({ saving: true })
    try {
      await resourcesService.update(resource.name, {
        name: newName,
        type: resource.type,
        subtype: resource.subtype,
        agent_id: resource.agent_id,
        env_vars: resource.env_vars || {},
      })
      if (current !== session) return
      set({ name: newName, resource: { ...resource, name: newName } })
      get().loadRoles()
    } finally {
      if (current === session) set({ saving: false })
    }
  },

  deleteResource: () => resourcesService.remove(get().resource.name),

  // Stays on the page and reloads after the DELETE ends (Q4). The row goes
  // first, so a failed reload cannot keep it.
  deleteRole: async (roleName) => {
    const current = session
    await connectionsService.deleteConnection(roleName)
    if (current !== session) return
    set((state) => {
      const items = state.roles.items.filter((role) => role.name !== roleName)
      const total = Math.max(0, state.roles.total - (state.roles.items.length - items.length))
      return { roles: { ...state.roles, items, total } }
    })
    await get().loadRoles()
  },

  reset: () => {
    session++
    resourceGen++
    rolesGen++
    set(initial)
  },
}))

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) useConfigureResourceStore.getState().reset()
})
