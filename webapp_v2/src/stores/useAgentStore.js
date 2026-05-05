import { create } from 'zustand'
import { agentsService } from '@/services/agents'

export const useAgentStore = create((set) => ({
  agents: [],
  loading: false,
  error: null,
  // { status: 'loading' | 'ready' | 'error', token: string | null }
  agentKey: null,

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

  clearAgentKey: () => set({ agentKey: null }),
}))
