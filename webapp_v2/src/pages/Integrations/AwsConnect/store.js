import { create } from 'zustand'
import { awsConnectService } from '@/services/awsConnect'
import { useAuthStore } from '@/stores/useAuthStore'
import { errorMessage } from './utils'

const initial = { jobs: [], loading: false, loaded: false, error: null, updatedAt: 0 }

// Module-level so a response that outlives reset() is still recognised as stale.
let generation = 0

export const useAwsJobsStore = create((set) => ({
  ...initial,

  fetchJobs: async () => {
    const id = ++generation
    set({ loading: true, error: null })
    try {
      const { data } = await awsConnectService.listJobs()
      if (id !== generation) return
      set({ jobs: data?.items ?? [], loading: false, loaded: true, updatedAt: Date.now() })
    } catch (err) {
      if (id !== generation) return
      set({ loading: false, loaded: true, error: errorMessage(err, 'Failed to load the discovery jobs.'), updatedAt: Date.now() })
    }
  },

  reset: () => {
    generation++
    set(initial)
  },
}))

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) useAwsJobsStore.getState().reset()
})
