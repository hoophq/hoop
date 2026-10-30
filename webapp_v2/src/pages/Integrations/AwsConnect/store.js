import { create } from 'zustand'
import { awsConnectService } from '@/services/awsConnect'
import { useAuthStore } from '@/stores/useAuthStore'
import { errorMessage, isRunning, POLL_INTERVAL_MS } from './utils'

const initial = { jobs: [], loading: false, loaded: false, error: null }

// Module-level so a response that outlives reset() is still recognised as stale.
let generation = 0
let polling = false
let timer = null

export const useAwsJobsStore = create((set, get) => {
  // Each answer arms the next poll while a job runs (Q28).
  const schedule = () => {
    clearTimeout(timer)
    timer = null
    if (polling && get().jobs.some(isRunning)) timer = setTimeout(() => get().fetchJobs(), POLL_INTERVAL_MS)
  }

  return {
    ...initial,

    fetchJobs: async () => {
      const id = ++generation
      set({ loading: true, error: null })
      try {
        const { data } = await awsConnectService.listJobs()
        if (id !== generation) return
        set({ jobs: data?.items ?? [], loading: false, loaded: true })
      } catch (err) {
        if (id !== generation) return
        set({ loading: false, loaded: true, error: errorMessage(err, 'Failed to load the discovery jobs.') })
      }
      schedule()
    },

    startPolling: () => {
      polling = true
      return get().fetchJobs()
    },

    stopPolling: () => {
      polling = false
      schedule()
    },

    reset: () => {
      generation++
      polling = false
      schedule()
      set(initial)
    },
  }
})

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) useAwsJobsStore.getState().reset()
})
