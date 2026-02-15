import { create } from 'zustand'

export const useUserStore = create((set) => ({
  user: null,
  loading: false,

  setUser: (user) => set({ user }),
  setLoading: (loading) => set({ loading }),
  clear: () => set({ user: null }),
}))
