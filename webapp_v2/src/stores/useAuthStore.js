import { create } from 'zustand'

export const useAuthStore = create((set) => ({
  token: localStorage.getItem('token') || null,
  isAuthenticated: !!localStorage.getItem('token'),

  setToken: (token) => {
    localStorage.setItem('token', token)
    set({ token, isAuthenticated: true })
  },

  logout: () => {
    localStorage.removeItem('token')
    set({ token: null, isAuthenticated: false })
  },
}))
