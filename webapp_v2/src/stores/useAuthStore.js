import { create } from 'zustand'
import { useUserStore } from '@/stores/useUserStore'

// Helper to get cookie value by name
const getCookieValue = (cookieName) => {
  if (typeof document === 'undefined') return null

  const cookies = document.cookie.split('; ')
  const targetCookie = cookies.find((cookie) => cookie.startsWith(`${cookieName}=`))
  return targetCookie ? targetCookie.substring(cookieName.length + 1) : null
}

// Helper to clear a cookie
const clearCookie = (cookieName) => {
  if (typeof document === 'undefined') return
  document.cookie = `${cookieName}=; max-age=0; path=/`
}

const REDIRECT_KEY = 'redirect-after-auth'

export const useAuthStore = create((set, get) => ({
  token: localStorage.getItem('jwt-token') || null,
  isAuthenticated: !!localStorage.getItem('jwt-token'),

  // Initialize token from cookies or query params (used on callback)
  initializeToken: () => {
    const searchParams = new URLSearchParams(window.location.search)
    const token = getCookieValue('hoop_access_token') || searchParams.get('token')

    if (token) {
      localStorage.setItem('jwt-token', token)
      clearCookie('hoop_access_token')
      set({ token, isAuthenticated: true })
      return token
    }

    return get().token
  },

  setToken: (token) => {
    localStorage.setItem('jwt-token', token)
    set({ token, isAuthenticated: true })
  },

  // Clearing the token is not enough. ProtectedRoute skips /userinfo when the user
  // store already holds someone, so a second sign-in in the same tab would run with the
  // previous account's identity, admin flag and license until a full reload.
  logout: () => {
    localStorage.removeItem('jwt-token')
    set({ token: null, isAuthenticated: false })
    useUserStore.getState().clear()
  },

  // Save current URL for redirect after auth. Per tab: sessionStorage survives
  // this tab's round trip to the IdP, and another tab's 401 cannot overwrite it
  // (an old tab with a stale token sent a new sign-in to its own page).
  saveRedirectUrl: (url) => {
    sessionStorage.setItem(REDIRECT_KEY, url)
  },

  // The saved URL, left in place: the login carries it to the IdP and back.
  peekRedirectUrl: () => sessionStorage.getItem(REDIRECT_KEY) || localStorage.getItem(REDIRECT_KEY),

  // Get and clear redirect URL. localStorage is where the legacy CLJS app saves
  // it, so it is read when this tab saved none.
  getAndClearRedirectUrl: () => {
    const url = sessionStorage.getItem(REDIRECT_KEY) || localStorage.getItem(REDIRECT_KEY)
    sessionStorage.removeItem(REDIRECT_KEY)
    localStorage.removeItem(REDIRECT_KEY)
    return url
  },
}))
