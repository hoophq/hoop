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

function sentToken(config) {
  return config?.headers?.Authorization?.replace(/^Bearer /, '')
}

function renew(response) {
  if (!response) return
  storeRenewedToken(sentToken(response.config), response.headers)
}

// Response interceptor: store a renewed token; handle 401. A 401 for a token
// that is no longer the stored one is retried once with the current token
// (the gateway rejects before any handler). One with the current token ends
// the session; one with no stored token follows a logout: nothing to do.
api.interceptors.response.use(
  (response) => {
    renew(response)
    return response
  },
  (error) => {
    renew(error.response)
    if (error.response?.status !== 401) return Promise.reject(error)

    const authStore = useAuthStore.getState()
    const sent = sentToken(error.config)
    const current = authStore.getToken()
    if (!current) return Promise.reject(error)
    if (sent && sent !== current && !error.config._retried) {
      error.config._retried = true
      error.config.headers.Authorization = `Bearer ${current}`
      return api.request(error.config)
    }

    // Save current URL for redirect after auth
    authStore.saveRedirectUrl(window.location.href)
    // Logout (clears token)
    authStore.logout()
    // Redirect to login
    window.location.href = '/login'
    return Promise.reject(error)
  },
)

export default api
