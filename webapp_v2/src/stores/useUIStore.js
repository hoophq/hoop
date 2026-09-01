import { create } from 'zustand'

const getSavedCollapsed = () => localStorage.getItem('sidebar') === 'closed'

// EVL-241: how long a dismissed setup checklist stays hidden.
const CONFIG_STATUS_DISMISS_KEY = 'config-status-dismissed'
// One task left is a decision, not a postponement: the admin read the last item
// and does not want it. Most reports are orgs stuck on "Set Protection Level".
const PERMANENT_AT_OR_BELOW = 1
const SNOOZE_MS = 30 * 24 * 60 * 60 * 1000

// Read once here, never inside a selector: Date.now() in a selector makes the
// snapshot unstable, and the ConfigStatus gate must stay pure and hook-free
// (DEP-136). A snooze therefore ends on the next page load, not mid-session.
const readConfigStatusDismiss = () => {
  try {
    const saved = JSON.parse(localStorage.getItem(CONFIG_STATUS_DISMISS_KEY))
    const { userId, until } = saved ?? {}
    if (!userId) return null
    if (until === null) return { userId, until }
    if (typeof until !== 'number' || until <= Date.now()) {
      localStorage.removeItem(CONFIG_STATUS_DISMISS_KEY)
      return null
    }
    return { userId, until }
  } catch {
    // Truncated or hand-edited value — fail toward showing the checklist.
    return null
  }
}

export const useUIStore = create((set) => ({
  sidebarOpen: false,
  sidebarCollapsed: getSavedCollapsed(),
  pendingOpenSection: null,
  // Keyed by user id so one admin dismissing the setup checklist cannot silence
  // it for another admin sharing the browser.
  configStatusDismiss: readConfigStatusDismiss(),

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

  dismissConfigStatus: (userId, subItemsLeft) => {
    const until = subItemsLeft <= PERMANENT_AT_OR_BELOW ? null : Date.now() + SNOOZE_MS
    const dismiss = { userId, until }
    localStorage.setItem(CONFIG_STATUS_DISMISS_KEY, JSON.stringify(dismiss))
    set({ configStatusDismiss: dismiss })
  },
}))
