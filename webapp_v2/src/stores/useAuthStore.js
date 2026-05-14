import { create } from 'zustand'

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

  logout: () => {
    localStorage.removeItem('jwt-token')
    set({ token: null, isAuthenticated: false })
  },

  // Save current URL for redirect after auth
  saveRedirectUrl: (url) => {
    localStorage.setItem('redirect-after-auth', url)
  },

  // Get and clear redirect URL
  getAndClearRedirectUrl: () => {
    const url = localStorage.getItem('redirect-after-auth')
    localStorage.removeItem('redirect-after-auth')
    return url
  },
}))
