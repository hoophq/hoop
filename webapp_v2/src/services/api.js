import axios from 'axios'
import { useAuthStore } from '@/stores/useAuthStore'

const api = axios.create({
  baseURL: import.meta.env.VITE_API_URL || '/api',
  headers: {
    'Content-Type': 'application/json',
  },
})

// Request interceptor: Add JWT token to all requests
api.interceptors.request.use((config) => {
  const token = useAuthStore.getState().token
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Response interceptor: Handle 401 by saving redirect URL and logging out
api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      const authStore = useAuthStore.getState()

      // Save current URL for redirect after auth
      const currentUrl = window.location.href
      authStore.saveRedirectUrl(currentUrl)

      // Logout (clears token)
      authStore.logout()

      // Redirect to login
      window.location.href = '/login'
    }
    return Promise.reject(error)
  },
)

export default api
