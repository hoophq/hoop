import api from './api'

const AUTH_ENDPOINTS = {
  LOCAL_LOGIN: '/localauth/login',
  LOCAL_REGISTER: '/localauth/register',
  IDPS_LOGIN: '/login',
  SIGNUP: '/signup',
  USER: '/users/me',
  PUBLIC_SERVER_INFO: '/publicserverinfo',
}

export const authService = {
  // Fetch public server info to determine auth method
  async getPublicServerInfo() {
    const response = await api.get(AUTH_ENDPOINTS.PUBLIC_SERVER_INFO)
    return response.data
  },

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
  // Calls GET /login?redirect=<callbackUrl> and returns login_url from response
  // Handles Microsoft Entra ID prompt difference (select_account vs login)
  async getLoginUrl(callbackUrl, options = {}) {
    const { promptLogin = false } = options

    const params = new URLSearchParams()

    if (promptLogin) {
      const idpProviderName = localStorage.getItem('idp-provider-name')
      const promptValue =
        idpProviderName === 'microsoft-entra-id' ? 'select_account' : 'login'
      params.append('prompt', promptValue)
    }

    params.append('redirect', callbackUrl)

    const response = await api.get(`${AUTH_ENDPOINTS.IDPS_LOGIN}?${params.toString()}`)
    return response.data.login_url
  },

  // Get signup URL
  async getSignupUrl(callbackUrl) {
    const params = new URLSearchParams({
      prompt: 'login',
      screen_hint: 'signup',
      redirect: callbackUrl,
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
