import { create } from 'zustand'
import { authService } from '@/services/auth'
import { usersService } from '@/services/users'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import { promptDismissKey } from './helpers'

// Without storage, "Not now" lasts until the page reloads.
function readDismissed(key) {
  try {
    return key && localStorage.getItem(key) ? key : null
  } catch {
    return null
  }
}

const initialState = {
  // The answer of GET /users/self/slack/link and the user it belongs to.
  status: null,
  statusUserId: null,
  modalOpened: false,
  // The dismiss key of the user that said "Not now" in this browser.
  dismissedKey: null,
}

// A late answer for a user that signed out is dropped.
let statusRequestId = 0
let confirming = false

export const useSlackConnectStore = create((set, get) => ({
  ...initialState,

  // Once per user: the layout that asks remounts on every React <-> CLJS move.
  loadStatus: async (userId) => {
    if (!userId || get().statusUserId === userId) return
    const requestId = ++statusRequestId
    const key = promptDismissKey(userId)
    set({ status: null, statusUserId: userId, dismissedKey: readDismissed(key) })
    try {
      const { data } = await usersService.getSlackLink()
      if (requestId !== statusRequestId) return
      set({ status: { available: Boolean(data?.available), linked: Boolean(data?.linked) } })
    } catch {
      // Unknown reads as not available: no prompt, no menu item. The next load asks again.
      if (requestId === statusRequestId) set({ statusUserId: null })
    }
  },

  openModal: () => set({ modalOpened: true }),
  closeModal: () => set({ modalOpened: false }),

  dismissPrompt: (userId) => {
    const key = promptDismissKey(userId)
    if (!key) return
    try {
      localStorage.setItem(key, 'not-now')
    } catch {
      // Kept in dismissedKey.
    }
    set({ dismissedKey: key })
  },

  // The live event and the poll both land here.
  confirmLinked: async () => {
    if (confirming) return
    confirming = true
    const requestId = statusRequestId
    try {
      const userData = await authService.getCurrentUser().catch(() => null)
      if (requestId !== statusRequestId) return
      if (userData?.id) useUserStore.getState().setUser(userData)
      set((state) => ({ status: { ...state.status, linked: true }, modalOpened: false }))
      showSnackbar({ level: 'success', text: 'Slack connected.' })
    } finally {
      confirming = false
    }
  },

  reset: () => {
    statusRequestId++
    set(initialState)
  },
}))

useAuthStore.subscribe((state, prev) => {
  if (state.token !== prev.token) useSlackConnectStore.getState().reset()
})
