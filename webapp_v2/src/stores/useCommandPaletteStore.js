import { create } from 'zustand'

export const useCommandPaletteStore = create((set) => ({
  currentPage: 'main',
  context: {},
  searchStatus: 'idle',
  searchResults: { resources: [], connections: [], runbooks: [] },

  navigateToPage: (page, context = {}) => set({ currentPage: page, context }),
  back: () => set({ currentPage: 'main', context: {} }),
  setSearchResults: (searchStatus, data) =>
    set({
      searchStatus,
      searchResults: data.resources !== undefined
        ? data
        : { resources: [], connections: [], runbooks: [] },
    }),
  reset: () => set({
    currentPage: 'main',
    context: {},
    searchStatus: 'idle',
    searchResults: { resources: [], connections: [], runbooks: [] },
  }),
}))
