import { create } from 'zustand'
import { attributesService } from '@/services/attributes'
import { connectionsService } from '@/services/connections'
import { pluginsService } from '@/services/plugins'
import { userGroupsService } from '@/services/userGroups'
import { usersService } from '@/services/users'
import { useAuthStore } from '@/stores/useAuthStore'
import {
  ACCESS_CONTROL_PLUGIN,
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

// Bumped by reset. A response to a request made before the bump belongs to the
// previous session (another account, or a replaced token) and is dropped.
let session = 0

// Role ids whose connection lookup is on the wire, so two expanded groups that
// share roles do not ask for them twice.
const pendingConnectionIds = new Set()

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

  submitting: false,
}

export const useAccessControlStore = create((set, get) => ({
  ...initialState,

  reset: () => {
    session += 1
    pendingConnectionIds.clear()
    set(initialState)
  },

  fetchPlugin: async () => {
    const started = session
    set({ pluginStatus: 'loading' })
    try {
      const { data } = await pluginsService.get(ACCESS_CONTROL_PLUGIN)
      if (started !== session) return
      set({ plugin: data, pluginStatus: 'success' })
    } catch (error) {
      if (started !== session) return
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
    const started = session
    set({ groupsStatus: 'loading' })
    try {
      const { data } = await userGroupsService.list()
      if (started !== session) return
      set({ groups: data ?? [], groupsStatus: 'success' })
    } catch {
      if (started !== session) return
      set({ groupsStatus: 'error' })
    }
  },

  fetchUsers: async () => {
    const started = session
    set({ usersStatus: 'loading' })
    try {
      const { data } = await usersService.list()
      if (started !== session) return
      set({ users: data ?? [], usersStatus: 'success' })
    } catch {
      if (started !== session) return
      set({ usersStatus: 'error' })
    }
  },

  fetchAttributes: async () => {
    const started = session
    set({ attributesStatus: 'loading' })
    try {
      const attributes = await attributesService.listAll()
      if (started !== session) return
      set({ attributes, attributesStatus: 'success' })
    } catch {
      if (started !== session) return
      set({ attributesStatus: 'error' })
    }
  },

  // Best effort: a failure here only costs the type icons, so it stays silent
  // rather than taking the whole list down.
  fetchConnections: async (ids) => {
    const missing = [...new Set(ids)].filter(
      (id) => !get().connectionsById[id] && !pendingConnectionIds.has(id),
    )
    if (missing.length === 0) return
    const started = session
    missing.forEach((id) => pendingConnectionIds.add(id))
    try {
      const batches = await Promise.all(
        chunk(missing, CONNECTIONS_CHUNK).map((batch) =>
          connectionsService.getConnectionsByIds(batch),
        ),
      )
      if (started !== session) return
      const resolved = {}
      for (const rows of batches) for (const row of rows ?? []) resolved[row.id] = row
      set((state) => ({ connectionsById: { ...state.connectionsById, ...resolved } }))
    } catch {
      // keep the fallback icon
    } finally {
      // A failed lookup stays retryable. After a reset the set is already clear.
      if (started === session) missing.forEach((id) => pendingConnectionIds.delete(id))
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

// The store holds the org's users and groups, so the next account in the same
// tab must not see them: reset on logout and on a token change.
useAuthStore.subscribe((state, prev) => {
  const loggedOut = prev.isAuthenticated && !state.isAuthenticated
  const tokenChanged = prev.token && state.token && prev.token !== state.token
  if (loggedOut || tokenChanged) {
    useAccessControlStore.getState().reset()
  }
})
