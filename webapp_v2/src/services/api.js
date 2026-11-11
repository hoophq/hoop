import axios from 'axios'
import { tokenSubject, useAuthStore } from '@/stores/useAuthStore'

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

// A 401: save the URL to come back to, log out and go to the login page. The
// fetch streams call it too: they do not pass through the interceptor.
export function redirectToLogin() {
  const authStore = useAuthStore.getState()
  authStore.saveRedirectUrl(window.location.href)
  authStore.logout()
  window.location.href = '/login'
}

// Response interceptor: store a renewed token; handle 401. A 401 for a token
// that is no longer the stored one is retried once with the current token
// (the gateway rejects before any handler), when both name the same account:
// another tab may have signed in as someone else, and their token must not
// run this request. One with the current token ends the session; one with no
// stored token follows a logout: nothing to do.
api.interceptors.response.use(
  (response) => {
    renew(response)
    return response
  },
  (error) => {
    renew(error.response)
    if (error.response?.status !== 401) return Promise.reject(error)

    const sent = sentToken(error.config)
    const current = useAuthStore.getState().getToken()
    if (!current) return Promise.reject(error)
    if (sent && sent !== current) {
      const subject = tokenSubject(sent)
      if (subject && subject === tokenSubject(current) && !error.config._retried) {
        error.config._retried = true
        error.config.headers.Authorization = `Bearer ${current}`
        return api.request(error.config)
      }
      return Promise.reject(error)
    }

    redirectToLogin()
    return Promise.reject(error)
  },
)

export default api
