import { create } from 'zustand'
import { agentsService } from '@/services/agents'

export const useAgentStore = create((set) => ({
  agents: [],
  loading: false,
  error: null,

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
    set({ loading: true, error: null })
    try {
      const { data } = await agentsService.create(agentData)
      set((state) => ({
        agents: [...state.agents, data],
        loading: false,
      }))
      return data
    } catch (error) {
      set({ error: error.message, loading: false })
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
}))
