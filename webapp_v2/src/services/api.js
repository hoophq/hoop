import axios from 'axios'
import { useAuthStore } from '@/stores/useAuthStore'

// Mirrors the CLJS get-api-url logic: use VITE_API_URL if set, otherwise build
// from window.location so the app works at any host/port without configuration.
function getApiUrl() {
  if (import.meta.env.VITE_API_URL) return import.meta.env.VITE_API_URL
  return `${window.location.protocol}//${window.location.host}/api`
}

const api = axios.create({
  baseURL: getApiUrl(),
  headers: {
    'Content-Type': 'application/json',
  },
})

// Request interceptor: Add JWT token to all requests
api.interceptors.request.use((config) => {
  const token = useAuthStore.getState().getToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// The gateway sends a renewed token in this header, on an error response too.
// Stored as the CLJS app does (webapp/src/webapp/http/request.cljs), so the
// session slides on React routes. `sent` is the token the request carried.
export function storeRenewedToken(sent, headers) {
  useAuthStore.getState().renewToken(sent, headers?.get('X-New-Access-Token'))
}

function renew(response) {
  if (!response) return
  const sent = response.config?.headers?.Authorization?.replace(/^Bearer /, '')
  storeRenewedToken(sent, response.headers)
}

// Response interceptor: store a renewed token; handle 401 by saving redirect URL and logging out
api.interceptors.response.use(
  (response) => {
    renew(response)
    return response
  },
  (error) => {
    renew(error.response)
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
