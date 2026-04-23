import { create } from 'zustand'

const getSavedCollapsed = () => localStorage.getItem('sidebar') === 'closed'

export const useUIStore = create((set) => ({
  sidebarOpen: false,
  sidebarCollapsed: getSavedCollapsed(),
  pendingOpenSection: null,

  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
  setSidebarOpen: (open) => set({ sidebarOpen: open }),

  toggleSidebarCollapsed: () =>
    set((state) => {
      const next = !state.sidebarCollapsed
      localStorage.setItem('sidebar', next ? 'closed' : 'opened')
      return { sidebarCollapsed: next }
    }),

  setPendingOpenSection: (label) => set({ pendingOpenSection: label }),
  clearPendingOpenSection: () => set({ pendingOpenSection: null }),
}))
