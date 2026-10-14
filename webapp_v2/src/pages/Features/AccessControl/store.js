import { create } from 'zustand'
import { attributesService } from '@/services/attributes'
import { connectionsService } from '@/services/connections'
import { pluginsService } from '@/services/plugins'
import { userGroupsService } from '@/services/userGroups'
import { usersService } from '@/services/users'
import { useAuthStore } from '@/stores/useAuthStore'
import {
  ACCESS_CONTROL_PLUGIN,
  PANEL_PAGE_SIZE,
  attributeGroupPayload,
  diffGroupAttributes,
  mergeGroupIntoConnections,
  removeGroupFromConnections,
} from './helpers'

const CONNECTIONS_CHUNK = 100 // /connections page_size cap

function chunk(items, size) {
  const out = []
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size))
  return out
}

// PUT /plugins/:name looks the plugin up by the name in the *body*, ignoring
// the path segment, and `connections` must be present — omitting it or sending
// null fails validation, while an empty array is how you clear every
// association.
function putConnections(connections) {
  return pluginsService.update(ACCESS_CONTROL_PLUGIN, {
    name: ACCESS_CONTROL_PLUGIN,
    connections,
  })
}

// Attribute rows are independent, so these run concurrently. A rejection still
// leaves the other writes applied — there is no transactional endpoint for
// this, so the caller surfaces the error and the admin retries.
function applyAttributeChanges({ attributes, groupName, selectedNames }) {
  const { added, removed } = diffGroupAttributes({ attributes, groupName, selectedNames })
  return Promise.all([
    ...added.map((a) =>
      attributesService.update(a.name, attributeGroupPayload(a, groupName, true)),
    ),
    ...removed.map((a) =>
      attributesService.update(a.name, attributeGroupPayload(a, groupName, false)),
    ),
  ])
}

const initialState = {
  plugin: null,
  // 'idle' | 'loading' | 'success' | 'error' | 'not-installed'
  pluginStatus: 'idle',

  groups: [],
  groupsStatus: 'idle',

  // Every user with its groups, for the user count and list of each group.
  users: [],
  usersStatus: 'idle',

  attributes: [],
  attributesStatus: 'idle',

  // id -> connection, for the resource roles listed under each group. The
  // plugin resolves their names but not their subtype, which is what picks the
  // type icon.
  connectionsById: {},

  // One page of the users who can open the role picked in the filter.
  roleUsers: { connectionId: null, items: [], total: 0, page: 1, status: 'idle' },
  roleUsersRequestId: 0,

  submitting: false,
}

export const useAccessControlStore = create((set, get) => ({
  ...initialState,

  reset: () => set(initialState),

  fetchPlugin: async () => {
    set({ pluginStatus: 'loading' })
    try {
      const { data } = await pluginsService.get(ACCESS_CONTROL_PLUGIN)
      set({ plugin: data, pluginStatus: 'success' })
    } catch (error) {
      // A 404 is how the gateway reports "this plugin was never enabled" — it
      // drives the promotion screen, not the error screen.
      if (error?.response?.status === 404) {
        set({ plugin: null, pluginStatus: 'not-installed' })
        return
      }
      set({ pluginStatus: 'error' })
    }
  },

  fetchGroups: async () => {
    set({ groupsStatus: 'loading' })
    try {
      const { data } = await userGroupsService.list()
      set({ groups: data ?? [], groupsStatus: 'success' })
    } catch {
      set({ groupsStatus: 'error' })
    }
  },

  fetchUsers: async () => {
    set({ usersStatus: 'loading' })
    try {
      const { data } = await usersService.list()
      set({ users: data ?? [], usersStatus: 'success' })
    } catch {
      set({ usersStatus: 'error' })
    }
  },

  // The filter can change before a page answers, so only the latest request
  // commits. A new role drops the old rows at once: they belong to another
  // role and must not show under this one's heading. A new page of the same
  // role keeps them until the next page arrives.
  fetchRoleUsers: async (connectionId, page = 1) => {
    const requestId = get().roleUsersRequestId + 1
    set((state) => ({
      roleUsersRequestId: requestId,
      roleUsers:
        state.roleUsers.connectionId === connectionId
          ? { ...state.roleUsers, page, status: 'loading' }
          : { connectionId, items: [], total: 0, page, status: 'loading' },
    }))
    try {
      const { data } = await connectionsService.listUsers(connectionId, {
        page,
        pageSize: PANEL_PAGE_SIZE,
      })
      if (get().roleUsersRequestId !== requestId) return
      set({
        roleUsers: {
          connectionId,
          items: data?.data ?? [],
          total: data?.pages?.total ?? 0,
          page,
          status: 'success',
        },
      })
    } catch {
      if (get().roleUsersRequestId !== requestId) return
      set({ roleUsers: { connectionId, items: [], total: 0, page, status: 'error' } })
    }
  },

  clearRoleUsers: () =>
    set((state) => ({
      roleUsersRequestId: state.roleUsersRequestId + 1,
      roleUsers: initialState.roleUsers,
    })),

  fetchAttributes: async () => {
    set({ attributesStatus: 'loading' })
    try {
      set({ attributes: await attributesService.listAll(), attributesStatus: 'success' })
    } catch {
      set({ attributesStatus: 'error' })
    }
  },

  // Best effort: a failure here only costs the type icons, so it stays silent
  // rather than taking the whole list down.
  fetchConnections: async (ids) => {
    const missing = [...new Set(ids)].filter((id) => !get().connectionsById[id])
    if (missing.length === 0) return
    try {
      const batches = await Promise.all(
        chunk(missing, CONNECTIONS_CHUNK).map((batch) =>
          connectionsService.getConnectionsByIds(batch),
        ),
      )
      const resolved = {}
      for (const rows of batches) for (const row of rows ?? []) resolved[row.id] = row
      set((state) => ({ connectionsById: { ...state.connectionsById, ...resolved } }))
    } catch {
      // keep the fallback icon
    }
  },

  installPlugin: async () => {
    set({ submitting: true })
    try {
      await pluginsService.create({ name: ACCESS_CONTROL_PLUGIN, connections: [] })
      await get().fetchPlugin()
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  // Creates the group when `isNew`, then rewrites its resource-role and
  // attribute associations. Reports whether the group itself was created so a
  // retry after a later step failed does not hit a 409 on a group that now
  // exists.
  saveGroup: async ({ name, connectionIds, attributeNames, isNew }) => {
    set({ submitting: true })
    let groupCreated = false
    try {
      if (isNew) {
        await userGroupsService.create({ name })
        groupCreated = true
      }
      // Re-read the plugin right before merging: the whole connections array is
      // replaced on write, so a snapshot taken at page load would silently
      // revert any group edited in the meantime.
      const { data: plugin } = await pluginsService.get(ACCESS_CONTROL_PLUGIN)
      await putConnections(
        mergeGroupIntoConnections({
          pluginConnections: plugin.connections,
          selectedConnectionIds: connectionIds,
          groupName: name,
        }),
      )
      await applyAttributeChanges({
        attributes: get().attributes,
        groupName: name,
        selectedNames: attributeNames,
      })
      set({ submitting: false })
      return { ok: true, groupCreated }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error, groupCreated }
    }
  },

  deleteGroup: async (name) => {
    set({ submitting: true })
    try {
      try {
        await userGroupsService.remove(name)
      } catch (error) {
        // A group can exist only in the plugin config — `allGroups` unions both
        // sources so those stay manageable. Deleting one 404s on the identity
        // side, which already satisfies the intent; the cleanup below is the
        // part that matters, and aborting here would strand the group forever.
        if (error?.response?.status !== 404) throw error
      }
      await applyAttributeChanges({
        attributes: get().attributes,
        groupName: name,
        selectedNames: [],
      })
      const { data: plugin } = await pluginsService.get(ACCESS_CONTROL_PLUGIN)
      await putConnections(
        removeGroupFromConnections({
          pluginConnections: plugin.connections,
          groupName: name,
        }),
      )
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },
}))

// The store holds the org's users and groups, so the next user in the same tab
// must not see them.
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) {
    useAccessControlStore.getState().reset()
  }
})
