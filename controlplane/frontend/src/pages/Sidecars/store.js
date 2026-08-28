import { create } from 'zustand'
// MOCK: delete this import and call sidecarsService.fleet() instead. See mock/index.js.
import { fleetMock } from './mock'

export const useSidecarsStore = create((set) => ({
  list: [],
  listStatus: 'idle', // 'idle' | 'loading' | 'success' | 'error'

  fetchList: async () => {
    set({ listStatus: 'loading' })
    try {
      // MOCK: swap for `await sidecarsService.fleet()` — same { data } shape.
      const { data } = await fleetMock()
      set({ list: data ?? [], listStatus: 'success' })
    } catch {
      set({ listStatus: 'error' })
    }
  },
}))
