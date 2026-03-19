import { create } from 'zustand'

const getSavedCollapsed = () => localStorage.getItem('sidebar') === 'closed'

export const useUIStore = create((set) => ({
  sidebarOpen: true,
  sidebarCollapsed: getSavedCollapsed(),

  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
  setSidebarOpen: (open) => set({ sidebarOpen: open }),

  toggleSidebarCollapsed: () =>
    set((state) => {
      const next = !state.sidebarCollapsed
      localStorage.setItem('sidebar', next ? 'closed' : 'opened')
      return { sidebarCollapsed: next }
    }),
}))
