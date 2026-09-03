import { create } from 'zustand'

const getSavedCollapsed = () => localStorage.getItem('sidebar') === 'closed'

// EVL-241: how long a dismissed setup checklist stays hidden.
const CONFIG_STATUS_DISMISS_KEY = 'config-status-dismissed'
// One task left is a decision, not a postponement: the admin read the last item
// and does not want it. Most reports are orgs stuck on "Set Protection Level".
const PERMANENT_AT_OR_BELOW = 1
const SNOOZE_MS = 7 * 24 * 60 * 60 * 1000

// One entry per user id, so two admins sharing a browser each keep their own
// dismissal. The value is null for permanent, an epoch-ms deadline otherwise.
//
// Read once here, never inside a selector: Date.now() in a selector makes the
// snapshot unstable, and the ConfigStatus gate must stay pure and hook-free
// (DEP-136). A snooze therefore ends on the next page load, not mid-session.
// Expired entries are dropped from the returned map and leave storage on the
// next dismiss, which keeps page load free of storage writes.
const readConfigStatusDismiss = () => {
  let saved
  try {
    saved = JSON.parse(localStorage.getItem(CONFIG_STATUS_DISMISS_KEY))
  } catch {
    // Truncated or hand-edited value — fail toward showing the checklist.
    return {}
  }
  if (saved === null || typeof saved !== 'object' || Array.isArray(saved)) return {}
  const now = Date.now()
  return Object.fromEntries(
    Object.entries(saved).filter(
      ([, until]) => until === null || (typeof until === 'number' && until > now)
    )
  )
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

  dismissConfigStatus: (userId, subItemsLeft) =>
    set((state) => {
      const until = subItemsLeft <= PERMANENT_AT_OR_BELOW ? null : Date.now() + SNOOZE_MS
      const next = { ...state.configStatusDismiss, [userId]: until }
      try {
        localStorage.setItem(CONFIG_STATUS_DISMISS_KEY, JSON.stringify(next))
      } catch (err) {
        // Storage blocked or over quota. Hiding it for this session beats a
        // dismiss button that throws and leaves the card on screen.
        console.warn('[useUIStore] could not persist the checklist dismissal:', err)
      }
      return { configStatusDismiss: next }
    }),
}))
