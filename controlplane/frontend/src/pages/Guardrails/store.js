import { create } from 'zustand'
import { guardrailsService } from '@/services/guardrails'

export const useGuardrailsStore = create((set) => ({
  list: [],
  listStatus: 'idle', // 'idle' | 'loading' | 'success' | 'error'

  active: null,
  activeStatus: 'idle',

  submitting: false,

  fetchList: async () => {
    set({ listStatus: 'loading' })
    try {
      const { data } = await guardrailsService.list()
      set({ list: data ?? [], listStatus: 'success' })
    } catch {
      set({ listStatus: 'error' })
    }
  },

  fetchActive: async (id) => {
    set({ active: null, activeStatus: 'loading' })
    try {
      const { data } = await guardrailsService.get(id)
      set({ active: data, activeStatus: 'success' })
    } catch {
      set({ activeStatus: 'error' })
    }
  },

  clearActive: () => set({ active: null, activeStatus: 'idle' }),

  createGuardrail: async (payload) => {
    set({ submitting: true })
    try {
      await guardrailsService.create(payload)
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  updateGuardrail: async (id, payload) => {
    set({ submitting: true })
    try {
      await guardrailsService.update(id, payload)
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  deleteGuardrail: async (id) => {
    set({ submitting: true })
    try {
      await guardrailsService.remove(id)
      set((state) => ({
        submitting: false,
        list: state.list.filter((g) => g.id !== id),
      }))
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },
}))
