import { create } from 'zustand'
import { agentsService } from '@/services/agents'

export const useAgentStore = create((set) => ({
  agents: [],
  loading: false,
  error: null,
  // { status: 'loading' | 'ready' | 'error', token: string | null }
  agentKey: null,
  // Tag suggestions from GET /agent-tags: the predefined agent tags plus
  // every key/value in use, as [{ key, value }].
  tagsPool: [],

  fetchAgents: async () => {
    set({ loading: true, error: null })
    try {
      const { data } = await agentsService.list()
      set({ agents: data, loading: false })
    } catch (error) {
      set({ error: error.message, loading: false })
    }
  },

  createAgent: async (agentData) => {
    set({ loading: true, error: null, agentKey: { status: 'loading', token: null } })
    try {
      const { data } = await agentsService.create(agentData)
      set((state) => ({
        agents: [...state.agents, data],
        loading: false,
        agentKey: { status: 'ready', token: data.token ?? null },
      }))
      return data
    } catch (error) {
      set({ error: error.message, loading: false, agentKey: { status: 'error', token: null } })
      throw error
    }
  },

  deleteAgent: async (id) => {
    try {
      await agentsService.delete(id)
      set((state) => ({
        agents: state.agents.filter((a) => a.id !== id),
      }))
    } catch (error) {
      set({ error: error.message })
      throw error
    }
  },

  // Replaces the tags of one agent. Throws on failure so the caller can
  // keep its editor open.
  updateAgentTags: async (id, tags) => {
    const { data } = await agentsService.updateTags(id, tags)
    set((state) => ({
      agents: state.agents.map((a) => (a.id === id ? { ...a, tags: data.tags } : a)),
    }))
    return data
  },

  // Suggestions are optional: on failure the editor still takes custom
  // keys and values, the same as Roles → Configure.
  fetchTagsPool: async () => {
    try {
      const { data } = await agentsService.listTags()
      set({ tagsPool: data?.items ?? [] })
    } catch {
      set({ tagsPool: [] })
    }
  },

  clearAgentKey: () => set({ agentKey: null }),
}))
