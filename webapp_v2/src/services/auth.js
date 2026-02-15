import api from './api'

const AUTH_ENDPOINTS = {
  LOCAL_LOGIN: '/localauth/login',
  LOCAL_REGISTER: '/localauth/register',
  IDPS_LOGIN: '/login',
  SIGNUP: '/signup',
  USER: '/users/me',
}

export const authService = {
  // Local auth login
  async loginLocal(email, password) {
    const response = await api.post(AUTH_ENDPOINTS.LOCAL_LOGIN, {
      email,
      password,
    })

    // Token comes in response headers
    const token = response.headers.token || response.headers.Token
    return { token, user: response.data }
  },

  // Local auth register
  async registerLocal(email, password, name) {
    const response = await api.post(AUTH_ENDPOINTS.LOCAL_REGISTER, {
      email,
      password,
      name,
    })

    const token = response.headers.token || response.headers.Token
    return { token, user: response.data }
  },

  // Get IDP login URL (for OAuth providers)
  async getLoginUrl(redirectUrl, options = {}) {
    const { promptLogin = false } = options

    const params = new URLSearchParams()
    if (promptLogin) params.append('prompt', 'login')
    params.append('redirect', redirectUrl)

    const response = await api.get(`${AUTH_ENDPOINTS.IDPS_LOGIN}?${params.toString()}`)
    return response.data.login_url
  },

  // Get signup URL
  async getSignupUrl(redirectUrl) {
    const params = new URLSearchParams({
      prompt: 'login',
      screen_hint: 'signup',
      redirect: redirectUrl,
    })

    const response = await api.get(`${AUTH_ENDPOINTS.IDPS_LOGIN}?${params.toString()}`)
    return response.data.login_url
  },

  // Get current user
  async getCurrentUser() {
    const response = await api.get(AUTH_ENDPOINTS.USER)
    return response.data
  },

  // Signup (organization setup after OAuth)
  async signup(orgName, profileName) {
    const response = await api.post(AUTH_ENDPOINTS.SIGNUP, {
      org_name: orgName,
      profile_name: profileName,
    })
    return response.data
  },
}

export default authService
