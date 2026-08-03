import { create } from 'zustand'
import { attributesService } from '@/services/attributes'
import { pluginsService } from '@/services/plugins'
import { userGroupsService } from '@/services/userGroups'
import {
  ACCESS_CONTROL_PLUGIN,
  attributeGroupPayload,
  diffGroupAttributes,
  mergeGroupIntoConnections,
  removeGroupFromConnections,
} from './helpers'

const ATTRIBUTES_PAGE_SIZE = 100 // gateway cap for /attributes

// The attribute picker drives a full-replace write, so a truncated list would
// let an admin silently drop associations they never saw. Walk every page.
async function fetchAllAttributes() {
  const all = []
  for (let page = 1; ; page += 1) {
    const { data } = await attributesService.list({
      page,
      page_size: ATTRIBUTES_PAGE_SIZE,
    })
    const rows = data?.data ?? []
    all.push(...rows)
    if (rows.length === 0 || all.length >= (data?.pages?.total ?? all.length)) return all
  }
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

export const useAccessControlStore = create((set, get) => ({
  plugin: null,
  // 'idle' | 'loading' | 'success' | 'error' | 'not-installed'
  pluginStatus: 'idle',

  groups: [],
  groupsStatus: 'idle',

  attributes: [],
  attributesStatus: 'idle',

  submitting: false,

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

  fetchAttributes: async () => {
    set({ attributesStatus: 'loading' })
    try {
      set({ attributes: await fetchAllAttributes(), attributesStatus: 'success' })
    } catch {
      set({ attributesStatus: 'error' })
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
