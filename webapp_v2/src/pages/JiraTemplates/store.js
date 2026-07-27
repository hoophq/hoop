import { create } from 'zustand'
import { jiraTemplatesService } from '@/services/jiraTemplates'
import { connectionTagsService } from '@/services/connectionTags'

export const useJiraTemplatesStore = create((set, get) => ({
  list: [],
  listStatus: 'idle', // 'idle' | 'loading' | 'success' | 'error'

  // null means the org has no Jira integration configured yet
  // (GET /integrations/jira returns 200 with an empty body in that case).
  integration: null,
  integrationStatus: 'idle',

  active: null,
  activeStatus: 'idle',

  tags: [],
  tagsStatus: 'idle',

  submitting: false,

  fetchList: async () => {
    set({ listStatus: 'loading' })
    try {
      const data = await jiraTemplatesService.list()
      set({ list: data ?? [], listStatus: 'success' })
    } catch {
      set({ listStatus: 'error' })
    }
  },

  fetchIntegration: async () => {
    set({ integrationStatus: 'loading' })
    try {
      const data = await jiraTemplatesService.getIntegration()
      const integration = data && Object.keys(data).length ? data : null
      set({ integration, integrationStatus: 'success' })
    } catch {
      set({ integrationStatus: 'error' })
    }
  },

  fetchActive: async (id) => {
    set({ active: null, activeStatus: 'loading' })
    try {
      const data = await jiraTemplatesService.get(id)
      set({ active: data, activeStatus: 'success' })
    } catch {
      set({ activeStatus: 'error' })
    }
  },

  clearActive: () => set({ active: null, activeStatus: 'idle' }),

  fetchConnectionTags: async () => {
    set({ tagsStatus: 'loading' })
    try {
      const data = await connectionTagsService.list()
      set({ tags: data?.items ?? [], tagsStatus: 'success' })
    } catch {
      set({ tagsStatus: 'error' })
    }
  },

  createTemplate: async (payload) => {
    set({ submitting: true })
    try {
      await jiraTemplatesService.create(payload)
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  updateTemplate: async (id, payload) => {
    set({ submitting: true })
    try {
      await jiraTemplatesService.update(id, payload)
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  deleteTemplate: async (id) => {
    set({ submitting: true })
    try {
      await jiraTemplatesService.remove(id)
      set((state) => ({
        submitting: false,
        list: state.list.filter((t) => t.id !== id),
      }))
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  // First save creates the integration (POST); subsequent saves update it (PUT).
  saveIntegration: async (payload) => {
    set({ submitting: true })
    try {
      const data = get().integration
        ? await jiraTemplatesService.updateIntegration(payload)
        : await jiraTemplatesService.createIntegration(payload)
      set({ submitting: false, integration: data ?? payload })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },
}))
