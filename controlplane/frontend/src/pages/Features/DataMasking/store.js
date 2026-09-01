import { create } from 'zustand'
import { dataMaskingService } from '@/services/dataMasking'

export const useDataMaskingStore = create((set) => ({
  list: [],
  listStatus: 'idle', // 'idle' | 'loading' | 'success' | 'error'

  active: null,
  activeStatus: 'idle',

  submitting: false,

  fetchList: async () => {
    set({ listStatus: 'loading' })
    try {
      const { data } = await dataMaskingService.list()
      set({ list: data ?? [], listStatus: 'success' })
    } catch {
      set({ listStatus: 'error' })
    }
  },

  fetchActive: async (id) => {
    set({ active: null, activeStatus: 'loading' })
    try {
      const { data } = await dataMaskingService.get(id)
      set({ active: data, activeStatus: 'success' })
    } catch {
      set({ activeStatus: 'error' })
    }
  },

  clearActive: () => set({ active: null, activeStatus: 'idle' }),

  createRule: async (payload) => {
    set({ submitting: true })
    try {
      await dataMaskingService.create(payload)
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  updateRule: async (id, payload) => {
    set({ submitting: true })
    try {
      await dataMaskingService.update(id, payload)
      set({ submitting: false })
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },

  deleteRule: async (id) => {
    set({ submitting: true })
    try {
      await dataMaskingService.remove(id)
      set((state) => ({
        submitting: false,
        list: state.list.filter((r) => r.id !== id),
      }))
      return { ok: true }
    } catch (error) {
      set({ submitting: false })
      return { ok: false, error }
    }
  },
}))
